// Copyright (c) 2025 Alexey Mayshev and contributors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package otter

import (
	"context"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/stats"
)

type retainedBlob struct{ data [1 << 10]byte }

func TestCache_InPlaceUpdateDoesNotRetainOldValues(t *testing.T) {
	t.Parallel()

	t.Run("pointer", func(t *testing.T) {
		t.Parallel()
		// kept in one atomic word
		testDoesNotRetainOldValues(t, func(b *retainedBlob) *retainedBlob { return b })
	})
	t.Run("inline", func(t *testing.T) {
		t.Parallel()
		// kept inline until the first update, then behind a pointer
		type wrapped struct {
			b *retainedBlob
			n int
		}
		testDoesNotRetainOldValues(t, func(b *retainedBlob) wrapped { return wrapped{b: b} })
	})
}

func testDoesNotRetainOldValues[V any](t *testing.T, wrap func(b *retainedBlob) V) {
	t.Helper()

	for _, updates := range []int{1, 5} {
		c := Must(&Options[int, V]{
			MaximumSize:      1000,
			ExpiryCalculator: ExpiryWriting[int, V](time.Hour),
		})

		const n = 100
		var collected atomic.Int64
		track := func() V {
			b := &retainedBlob{}
			runtime.AddCleanup(b, func(_ int) { collected.Add(1) }, 0)
			return wrap(b)
		}
		for i := 0; i < n; i++ {
			c.Set(i, track())
		}
		for u := 0; u < updates; u++ {
			for i := 0; i < n; i++ {
				c.Set(i, track())
			}
		}
		c.CleanUp()

		// every value except the current one of each key must be collectable
		want := int64(n * updates)
		require.Eventually(t, func() bool {
			runtime.GC()
			return collected.Load() == want
		}, 5*time.Second, 10*time.Millisecond, "updates=%d: collected %d of %d", updates, collected.Load(), want)

		for i := 0; i < n; i++ {
			_, ok := c.GetIfPresent(i)
			require.True(t, ok)
		}
		runtime.KeepAlive(c)
	}
}

func TestCache_InPlaceUpdateConcurrentReads(t *testing.T) {
	t.Parallel()

	type pair struct {
		a, b int
		s    string
	}

	c := Must(&Options[int, pair]{
		MaximumSize:      1000,
		ExpiryCalculator: ExpiryAccessing[int, pair](time.Hour),
	})
	const keys = 64
	for i := 0; i < keys; i++ {
		c.Set(i, pair{a: 0, b: 0, s: "0"})
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				for i := 0; i < keys; i++ {
					v, ok := c.GetIfPresent(i)
					if !ok {
						continue
					}
					// a torn read would break the invariant between the fields
					if v.a != v.b || v.s == "" {
						t.Errorf("torn value: %+v", v)
						return
					}
				}
			}
		}()
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 1; j <= 2000; j++ {
				k := (j + w) % keys
				c.Compute(k, func(old pair, found bool) (pair, ComputeOp) {
					n := old.a + 1
					return pair{a: n, b: n, s: "x"}, WriteOp
				})
			}
		}(w)
	}
	time.Sleep(200 * time.Millisecond)
	stop.Store(true)
	wg.Wait()

	// the first update boxed every node (a pair is kept inline), later ones were applied in place
	for i := 0; i < keys; i++ {
		n := c.cache.hashmap.Get(i)
		require.NotNil(t, n)
		require.True(t, n.CanSetValue(), "key %d", i)
	}
}

// The timer wheel reads a node's expiration time before the hash table's lock is taken. A
// writer that extends the node in place in between must keep it in the cache.
func TestCache_ExpireNodeRechecksExpiration(t *testing.T) {
	t.Parallel()

	for _, extended := range []bool{true, false} {
		clk := newNonTickingClock()
		c := Must(&Options[int, int]{
			MaximumSize:      10,
			Clock:            clk,
			ExpiryCalculator: ExpiryWriting[int, int](time.Minute),
			Executor: func(fn func()) {
				fn()
			},
		})
		c.Set(1, 1)
		c.Set(1, 2) // an update, applied in place
		n := c.cache.hashmap.Get(1)

		clk.Sleep(2 * time.Minute)
		now := clk.NowNano()

		ci := c.cache
		ci.evictionMutex.Lock()
		// what the wheel does before calling expireNode: it found n expired and unlinked it
		ci.expirationPolicy.Delete(n)
		if extended {
			// a writer extends n in place before expireNode takes the hash table's lock
			n.SetExpiresAt(now + int64(time.Minute))
		}
		ci.expireNode(n, now)
		ci.evictionMutex.Unlock()

		v, ok := c.GetIfPresent(1)
		require.Equal(t, extended, ok, "extended=%v", extended)
		if extended {
			require.Equal(t, 2, v)
		}
		validateCache(t, c)
	}
}

// Once the bucket lock is released, a concurrent writer can update the node in place, so
// Compute must return the value it computed rather than read the node again.
func TestCache_ComputeReturnsItsOwnValue(t *testing.T) {
	t.Parallel()

	c := Must(&Options[int, int]{MaximumSize: 100})
	c.Set(1, 0)

	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			c.Set(1, -1)
		}
	}()
	defer func() {
		stop.Store(true)
		wg.Wait()
	}()

	for i := 1; i <= 100_000; i++ {
		v, ok := c.Compute(1, func(oldValue int, found bool) (int, ComputeOp) {
			return i, WriteOp
		})
		if !ok || v != i {
			t.Fatalf("Compute wrote %d, but returned %d (ok=%v)", i, v, ok)
		}
	}
}

type countingExpiry struct {
	creates, updates atomic.Int64
}

func (e *countingExpiry) ExpireAfterCreate(entry Entry[int, int]) time.Duration {
	e.creates.Add(1)
	return time.Duration(entry.Value) * time.Second
}

func (e *countingExpiry) ExpireAfterUpdate(entry Entry[int, int], oldValue int) time.Duration {
	e.updates.Add(1)
	return time.Duration(entry.Value) * time.Second
}

func (e *countingExpiry) ExpireAfterRead(entry Entry[int, int]) time.Duration {
	return entry.ExpiresAfter()
}

// When an update cannot be applied in place, the node that replaces the old one must reuse
// what the user's callbacks returned instead of calling them a second time.
func TestCache_FailedInPlaceUpdateCallsCallbacksOnce(t *testing.T) {
	t.Parallel()

	t.Run("weight changes", func(t *testing.T) {
		t.Parallel()

		var weighs atomic.Int64
		c := Must(&Options[int, int]{
			MaximumWeight: 1000,
			Weigher: func(key, value int) uint32 {
				weighs.Add(1)
				return uint32(value)
			},
		})
		c.Set(1, 5)

		weighs.Store(0)
		c.Set(1, 7) // the weight changes
		require.Equal(t, int64(1), weighs.Load())
		v, ok := c.GetIfPresent(1)
		require.True(t, ok)
		require.Equal(t, 7, v)
	})
	t.Run("expiration shrinks", func(t *testing.T) {
		t.Parallel()

		clk := newNonTickingClock()
		expiry := &countingExpiry{}
		c := Must(&Options[int, int]{
			MaximumSize:      10,
			Clock:            clk,
			ExpiryCalculator: expiry,
		})
		c.Set(1, 100)

		expiry.updates.Store(0)
		c.Set(1, 10) // the deadline moves earlier: the in-place attempt fails
		require.Equal(t, int64(1), expiry.updates.Load())
		e, ok := c.GetEntryQuietly(1)
		require.True(t, ok)
		require.Equal(t, 10, e.Value)
		require.Equal(t, clk.NowNano()+int64(10*time.Second), e.ExpiresAtNano)
	})
}

// Replacement notifications of an in-place update must carry the replaced value, fire once
// per listener, and the synchronous listener must run before readers can see the new value,
// as it does when the node is replaced.
func TestCache_InPlaceUpdateNotifications(t *testing.T) {
	t.Parallel()

	var (
		mu           sync.Mutex
		atomicEvents []DeletionEvent[int, int]
		events       []DeletionEvent[int, int]
		visible      []int
	)
	var c *Cache[int, int]
	c = Must(&Options[int, int]{
		MaximumSize: 10,
		Executor: func(fn func()) {
			fn()
		},
		OnAtomicDeletion: func(e DeletionEvent[int, int]) {
			// what a lock-free reader sees while the synchronous listener runs
			n := c.cache.hashmap.Get(e.Key)
			mu.Lock()
			defer mu.Unlock()
			atomicEvents = append(atomicEvents, e)
			visible = append(visible, n.Value())
		},
		OnDeletion: func(e DeletionEvent[int, int]) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, e)
		},
	})

	c.Set(1, 1)
	c.Set(1, 2) // in place: an int is kept in one atomic word
	c.Set(1, 3) // in place
	v, ok := c.Compute(1, func(oldValue int, found bool) (int, ComputeOp) {
		return 4, WriteOp
	}) // in place
	require.True(t, ok)
	require.Equal(t, 4, v)

	mu.Lock()
	defer mu.Unlock()
	want := []DeletionEvent[int, int]{
		{Key: 1, Value: 1, Cause: CauseReplacement},
		{Key: 1, Value: 2, Cause: CauseReplacement},
		{Key: 1, Value: 3, Cause: CauseReplacement},
	}
	require.Equal(t, want, atomicEvents)
	require.Equal(t, want, events)
	require.Equal(t, []int{1, 2, 3}, visible, "readers saw the new value before OnAtomicDeletion ran")
}

type recordingRefresh struct {
	mu        sync.Mutex
	expiresAt []int64
}

func (r *recordingRefresh) RefreshAfterCreate(entry Entry[int, int]) time.Duration {
	return time.Hour
}

func (r *recordingRefresh) RefreshAfterUpdate(entry Entry[int, int], oldValue int) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expiresAt = append(r.expiresAt, entry.ExpiresAtNano)
	return time.Hour
}

func (r *recordingRefresh) RefreshAfterReload(entry Entry[int, int], oldValue int) time.Duration {
	return entry.RefreshableAfter()
}

func (r *recordingRefresh) RefreshAfterReloadFailure(entry Entry[int, int], err error) time.Duration {
	return entry.RefreshableAfter()
}

// RefreshAfterUpdate must see the entry's new expiration time whether the node is replaced
// or updated in place.
func TestCache_RefreshAfterUpdateSeesNewExpiration(t *testing.T) {
	t.Parallel()

	clk := newNonTickingClock()
	refresh := &recordingRefresh{}
	c := Must(&Options[int, int]{
		MaximumSize:       10,
		Clock:             clk,
		ExpiryCalculator:  &countingExpiry{}, // the value is the TTL in seconds
		RefreshCalculator: refresh,
	})
	now := clk.NowNano()

	c.Set(1, 100)
	c.Set(1, 300) // the node is replaced
	c.Set(1, 500) // in place

	refresh.mu.Lock()
	defer refresh.mu.Unlock()
	require.Equal(t, []int64{now + int64(300*time.Second), now + int64(500*time.Second)}, refresh.expiresAt)
}

// Caches without maintenance never update entries in place, so their nodes must not carry
// the value pointer.
func TestCache_UnmaintainedNodesHaveNoValuePointer(t *testing.T) {
	t.Parallel()

	require.Equal(t, unsafe.Sizeof(struct{ key, value int }{}), unsafe.Sizeof(node.B[int, int]{}))
	require.Equal(t, unsafe.Sizeof(struct {
		key, value    int
		refreshableAt atomic.Int64
	}{}), unsafe.Sizeof(node.BR[int, int]{}))

	c := Must(&Options[int, int]{})
	c.Set(1, 1)
	c.Set(1, 2)
	n := c.cache.hashmap.Get(1)
	require.False(t, n.CanSetValue())
	require.Equal(t, 2, n.Value())
}

// readHookExpiry expires entries a fixed time after every write and calls onRead from
// ExpireAfterRead, between the moment a read takes its snapshot of the entry and the moment
// it stores the deadline derived from it.
type readHookExpiry struct {
	ttl    time.Duration
	onRead func()
}

func (e *readHookExpiry) ExpireAfterCreate(Entry[int, int]) time.Duration { return e.ttl }

func (e *readHookExpiry) ExpireAfterUpdate(Entry[int, int], int) time.Duration { return e.ttl }

func (e *readHookExpiry) ExpireAfterRead(entry Entry[int, int]) time.Duration {
	if fn := e.onRead; fn != nil {
		e.onRead = nil
		fn()
	}
	return entry.ExpiresAfter()
}

// A read must not revert a deadline that a write stored after the read took its snapshot of the
// entry: the read's deadline is derived from the previous value.
func TestCache_ReadDoesNotRevertConcurrentDeadline(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		write func(c *Cache[int, int])
		want  time.Duration
	}{
		{
			name:  "in-place update",
			write: func(c *Cache[int, int]) { c.Set(1, 3) },
			want:  time.Hour,
		},
		{
			name:  "SetExpiresAfter",
			write: func(c *Cache[int, int]) { c.SetExpiresAfter(1, 2*time.Hour) },
			want:  2 * time.Hour,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clk := newNonTickingClock()
			expiry := &readHookExpiry{ttl: time.Hour}
			c := Must(&Options[int, int]{
				MaximumSize:      10,
				Clock:            clk,
				ExpiryCalculator: expiry,
			})
			c.Set(1, 1)
			c.Set(1, 2) // boxes the node, so that later writes are applied in place

			clk.Sleep(30 * time.Minute)
			expiry.onRead = func() { tt.write(c) }
			_, ok := c.GetIfPresent(1)
			require.True(t, ok)

			entry, ok := c.GetEntryQuietly(1)
			require.True(t, ok)
			require.Equal(t, tt.want, entry.ExpiresAfter())
		})
	}
}

// A changed weight is applied in place and reaches the eviction policy.
func TestCache_InPlaceWeightChange(t *testing.T) {
	t.Parallel()

	c := Must(&Options[int, int]{
		MaximumWeight: 100,
		Weigher: func(key, value int) uint32 {
			return uint32(value)
		},
		Executor: func(fn func()) {
			fn()
		},
	})
	for k := 1; k <= 5; k++ {
		c.Set(k, 10)
	}
	n := c.cache.hashmap.Get(1)

	c.Set(1, 30) // heavier, still within the maximum
	c.CleanUp()
	require.True(t, n.AsPointer() == c.cache.hashmap.Get(1).AsPointer(), "the node was replaced")
	require.Equal(t, uint64(70), c.WeightedSize())
	validateCache(t, c)

	c.Set(1, 5) // lighter
	c.CleanUp()
	require.Equal(t, uint64(45), c.WeightedSize())
	validateCache(t, c)

	c.Set(1, 90) // the cache now exceeds its maximum and must evict
	c.CleanUp()
	require.LessOrEqual(t, c.WeightedSize(), uint64(100))
	validateCache(t, c)

	c.Set(2, 1000) // heavier than the maximum: the entry itself is evicted
	c.CleanUp()
	_, ok := c.GetIfPresent(2)
	require.False(t, ok)
	require.LessOrEqual(t, c.WeightedSize(), uint64(100))
	validateCache(t, c)
}

type deletionRecorder struct {
	mu     sync.Mutex
	events []DeletionEvent[int, int]
}

func (r *deletionRecorder) record(e DeletionEvent[int, int]) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *deletionRecorder) get() []DeletionEvent[int, int] {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// An update that moves the expiration time earlier is applied in place, and the entry still
// expires on time: the timer wheel alone only reschedules a deadline that moved later.
func TestCache_InPlaceUpdateShrinksExpiration(t *testing.T) {
	t.Parallel()

	clk := newNonTickingClock()
	deleted := &deletionRecorder{}
	c := Must(&Options[int, int]{
		MaximumSize:      10,
		Clock:            clk,
		ExpiryCalculator: &countingExpiry{}, // the value is the TTL in seconds
		OnDeletion:       deleted.record,
		Executor: func(fn func()) {
			fn()
		},
	})
	c.Set(1, 100)
	n := c.cache.hashmap.Get(1)

	c.Set(1, 10) // the entry now expires in 10s instead of 100s
	require.True(t, n.AsPointer() == c.cache.hashmap.Get(1).AsPointer(), "the node was replaced")
	validateCache(t, c)

	clk.Sleep(20 * time.Second)
	c.CleanUp()
	require.Nil(t, c.cache.hashmap.Get(1), "the expired entry is still in the table")
	require.Contains(t, deleted.get(), DeletionEvent[int, int]{Key: 1, Value: 10, Cause: CauseExpiration})
	validateCache(t, c)
}

// A write over an expired entry that is still in the table reuses its node, and reports the
// old value as expired rather than replaced.
func TestCache_InPlaceUpdateResurrectsExpiredEntry(t *testing.T) {
	t.Parallel()

	clk := newNonTickingClock()
	deleted := &deletionRecorder{}
	atomicDeleted := &deletionRecorder{}
	counter := stats.NewCounter()
	// Maintenance is deferred: a read of an expired entry schedules it, and it would remove the
	// entry before the write, which is correct but not what this test is about.
	var pending []func()
	runPending := func() {
		for len(pending) > 0 {
			fn := pending[0]
			pending = pending[1:]
			fn()
		}
	}
	c := Must(&Options[int, int]{
		MaximumSize:      10,
		Clock:            clk,
		ExpiryCalculator: ExpiryWriting[int, int](time.Minute),
		StatsRecorder:    counter,
		OnDeletion:       deleted.record,
		OnAtomicDeletion: atomicDeleted.record,
		Executor: func(fn func()) {
			pending = append(pending, fn)
		},
	})
	updatable := func(key int) node.Node[int, int] {
		c.Set(key, 1)
		c.Set(key, 2) // an update, applied in place
		return c.cache.hashmap.Get(key)
	}
	n1, n2, n3, n4 := updatable(1), updatable(2), updatable(3), updatable(4)
	runPending()
	clk.Sleep(2 * time.Minute) // all four expire, but stay in the table

	c.Set(1, 10)
	v, ok := c.SetIfAbsent(2, 20)
	require.True(t, ok, "SetIfAbsent over an expired entry must write")
	require.Equal(t, 20, v)
	missesBefore := counter.Snapshot().Misses
	v, ok = c.Compute(3, func(oldValue int, found bool) (int, ComputeOp) {
		require.False(t, found)
		return 30, WriteOp
	})
	require.True(t, ok)
	require.Equal(t, 30, v)
	require.Equal(t, missesBefore+1, counter.Snapshot().Misses, "Compute over an expired entry is a miss")
	v, err := c.Get(context.Background(), 4, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
		return 40, nil
	}))
	require.NoError(t, err)
	require.Equal(t, 40, v)

	for key, n := range map[int]node.Node[int, int]{1: n1, 2: n2, 3: n3, 4: n4} {
		require.True(t, n.AsPointer() == c.cache.hashmap.Get(key).AsPointer(), "key %d: the node was replaced", key)
		v, ok := c.GetIfPresent(key)
		require.True(t, ok, "key %d", key)
		require.Equal(t, key*10, v)
	}
	runPending()
	for _, events := range [][]DeletionEvent[int, int]{deleted.get(), atomicDeleted.get()} {
		for key := 1; key <= 4; key++ {
			require.Contains(t, events, DeletionEvent[int, int]{Key: key, Value: 2, Cause: CauseExpiration})
		}
	}

	c.CleanUp()
	runPending()
	validateCache(t, c)
}

// A refresh of an entry that can be updated in place is applied in place.
func TestCache_InPlaceRefresh(t *testing.T) {
	t.Parallel()

	c := Must(&Options[int, int]{
		MaximumSize:       10,
		RefreshCalculator: RefreshWriting[int, int](time.Hour),
		Executor: func(fn func()) {
			fn()
		},
	})
	c.Set(1, 1)
	c.Set(1, 2) // an update, applied in place
	n := c.cache.hashmap.Get(1)

	res := <-c.Refresh(context.Background(), 1, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
		return 42, nil
	}))
	require.NoError(t, res.Err)
	require.Equal(t, 42, res.Value)
	require.True(t, n.AsPointer() == c.cache.hashmap.Get(1).AsPointer(), "the node was replaced")
	v, ok := c.GetIfPresent(1)
	require.True(t, ok)
	require.Equal(t, 42, v)
	validateCache(t, c)
}

// The access that an in-place update reports goes through the lossy read buffer and may be
// dropped. The reconciliation task alone must move a node whose deadline moved earlier to the
// right bucket of the timer wheel.
func TestCache_ReconcileReschedulesEarlierExpiration(t *testing.T) {
	t.Parallel()

	clk := newNonTickingClock()
	c := Must(&Options[int, int]{
		MaximumSize:      10,
		Clock:            clk,
		ExpiryCalculator: &countingExpiry{}, // the value is the TTL in seconds
		Executor: func(fn func()) {
			fn()
		},
	})
	c.Set(1, 100)
	ci := c.cache
	n := ci.hashmap.Get(1)

	ci.evictionMutex.Lock()
	ci.maintenance(nil)
	// an in-place update moved the deadline from 100s to 10s, and its access was dropped
	n.SetExpiresAt(clk.NowNano() + int64(10*time.Second))
	ci.runTask(ci.getTask(n, nil, reconcileReason, causeUnknown))
	ci.evictionMutex.Unlock()

	clk.Sleep(20 * time.Second)
	c.CleanUp()
	require.Nil(t, ci.hashmap.Get(1), "the expired entry is still in the table")
	validateCache(t, c)
}

// A reconciliation task that runs after the wheel has passed the node's new, earlier deadline
// still expires the node on the next advance instead of scheduling it days ahead.
func TestCache_ReconcileAfterDeadlinePassed(t *testing.T) {
	t.Parallel()

	clk := newNonTickingClock()
	c := Must(&Options[int, int]{
		MaximumSize:      10,
		Clock:            clk,
		ExpiryCalculator: &countingExpiry{}, // the value is the TTL in seconds
		Executor: func(fn func()) {
			fn()
		},
	})
	c.Set(1, 100)
	ci := c.cache
	n := ci.hashmap.Get(1)

	ci.evictionMutex.Lock()
	// an in-place update moved the deadline from 100s to 10s, and a maintenance pass advanced
	// the wheel past it before the task ran
	n.SetExpiresAt(clk.NowNano() + int64(10*time.Second))
	clk.Sleep(20 * time.Second)
	ci.maintenance(nil)
	ci.runTask(ci.getTask(n, nil, reconcileReason, causeUnknown))
	ci.evictionMutex.Unlock()

	clk.Sleep(2 * time.Second)
	c.CleanUp()
	require.Nil(t, ci.hashmap.Get(1), "the expired entry is still in the table")
	validateCache(t, c)
}

// One in-place update, or one write over an expired entry, can change both the weight and the
// deadline; a single reconciliation must fix both.
func TestCache_InPlaceWeightAndExpirationChange(t *testing.T) {
	t.Parallel()

	clk := newNonTickingClock()
	deleted := &deletionRecorder{}
	c := Must(&Options[int, int]{
		MaximumWeight: 1000,
		// the value is both the weight and the TTL in seconds
		Weigher: func(key, value int) uint32 {
			return uint32(value)
		},
		Clock:            clk,
		ExpiryCalculator: &countingExpiry{},
		OnDeletion:       deleted.record,
		Executor: func(fn func()) {
			fn()
		},
	})
	c.Set(1, 100)
	c.Set(2, 100)
	n := c.cache.hashmap.Get(1)

	c.Set(1, 10) // lighter, and expires in 10s instead of 100s
	c.CleanUp()
	require.True(t, n.AsPointer() == c.cache.hashmap.Get(1).AsPointer(), "the node was replaced")
	require.Equal(t, uint64(110), c.WeightedSize())
	validateCache(t, c)

	clk.Sleep(20 * time.Second)
	c.CleanUp()
	require.Nil(t, c.cache.hashmap.Get(1), "the expired entry is still in the table")
	require.Equal(t, uint64(100), c.WeightedSize())
	require.Contains(t, deleted.get(), DeletionEvent[int, int]{Key: 1, Value: 10, Cause: CauseExpiration})
	validateCache(t, c)

	// an expired entry that is still in the table is written again with another weight
	c.Set(3, 50)
	n = c.cache.hashmap.Get(3)
	clk.Sleep(51 * time.Second)
	c.Set(3, 30)
	c.CleanUp()
	require.True(t, n.AsPointer() == c.cache.hashmap.Get(3).AsPointer(), "the node was replaced")
	require.Contains(t, deleted.get(), DeletionEvent[int, int]{Key: 3, Value: 50, Cause: CauseExpiration})
	require.Equal(t, uint64(130), c.WeightedSize())
	validateCache(t, c)
}

// Both deletion listeners report the cause decided under the lock, even if the deadline of the
// replaced node is changed after the lock is released by a reader that still holds it.
func TestCache_DeletionListenersAgreeOnCause(t *testing.T) {
	t.Parallel()

	clk := newNonTickingClock()
	var (
		mu                     sync.Mutex
		deleted, atomicDeleted []DeletionEvent[int, string]
	)
	var c *Cache[int, string]
	c = Must(&Options[int, string]{
		MaximumSize:      10,
		Clock:            clk,
		ExpiryCalculator: ExpiryWriting[int, string](10 * time.Second),
		OnDeletion: func(e DeletionEvent[int, string]) {
			mu.Lock()
			defer mu.Unlock()
			deleted = append(deleted, e)
		},
		OnAtomicDeletion: func(e DeletionEvent[int, string]) {
			mu.Lock()
			atomicDeleted = append(atomicDeleted, e)
			mu.Unlock()
			// the hash table still returns the old node, whose deadline is extended
			c.SetExpiresAfter(e.Key, time.Hour)
		},
		Executor: func(fn func()) {
			fn()
		},
	})

	c.Set(1, "a") // a string is kept inline, so the next write replaces the node
	clk.Sleep(20 * time.Second)
	c.Set(1, "b")
	c.CleanUp()

	c.Set(2, "a")
	c.Invalidate(2)
	c.CleanUp()

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, atomicDeleted, deleted)
	require.Equal(t, []DeletionEvent[int, string]{
		{Key: 1, Value: "a", Cause: CauseExpiration},
		{Key: 2, Value: "a", Cause: CauseInvalidation},
	}, deleted)
}

// An entry whose weight a writer changes to zero in place, after a maintenance pass replayed the
// write buffer and before it evicts, must not be evicted for size on its stale accounted weight.
func TestCache_InPlaceZeroWeightIsNotEvicted(t *testing.T) {
	t.Parallel()

	c := Must(&Options[int, int]{
		MaximumWeight: 10,
		// a negative value pins the entry: it weighs nothing
		Weigher: func(_ int, v int) uint32 {
			if v < 0 {
				return 0
			}
			return 1
		},
		Executor: func(fn func()) {
			fn()
		},
	})
	ci := c.cache
	for k := 1; k <= 10; k++ {
		c.Set(k, k)
	}
	for k := 1; k <= 10; k++ {
		c.Set(k, k+1000) // boxes the nodes, so that later writes are applied in place
	}
	c.CleanUp()

	p := ci.evictionPolicy
	victim := p.probation.Head().Key()
	// make the window's head a frequent candidate, so that admission evicts the victim
	for i := 0; i < 4; i++ {
		c.GetIfPresent(p.window.Head().Key())
	}
	c.CleanUp()

	ci.evictionMutex.Lock()
	ci.drainReadBuffer()
	c.Set(100, 100)
	ci.drainWriteBuffer() // the insertion is replayed: 11 > 10
	c.Set(victim, -1)     // weight 1 -> 0 in place; its reweigh task is still buffered
	require.Equal(t, uint32(0), ci.hashmap.Get(victim).Weight())
	ci.evictNodes()
	ci.evictionMutex.Unlock()
	c.CleanUp()

	v, ok := c.GetIfPresent(victim)
	require.True(t, ok, "the zero-weight entry %d was evicted for size", victim)
	require.Equal(t, -1, v)
	require.LessOrEqual(t, c.WeightedSize(), uint64(10))
	validateCache(t, c)
}

// barrierReloader reloads a key only once parties reloads are in progress at the same time.
type barrierReloader struct {
	arrived sync.WaitGroup
}

func (l *barrierReloader) Load(context.Context, int) (int, error) { return 0, ErrNotFound }

func (l *barrierReloader) Reload(_ context.Context, key, _ int) (int, error) {
	l.arrived.Done()
	l.arrived.Wait()
	return key * 10, nil
}

// A refresh runs as an executor task. When it updates the entry in place, its deletion event
// must not be submitted to the executor as one more task: with an executor that runs at most N
// tasks and blocks the submitter, N concurrent refreshes would wait for each other's slots forever.
func TestCache_RefreshInPlaceWithBoundedExecutor(t *testing.T) {
	t.Parallel()

	const slots = 2
	sem := make(chan struct{}, slots)
	clk := newNonTickingClock()
	var events atomic.Int64
	c := Must(&Options[int, int]{
		MaximumSize:       100,
		Clock:             clk,
		RefreshCalculator: RefreshWriting[int, int](time.Minute),
		OnDeletion: func(DeletionEvent[int, int]) {
			events.Add(1)
		},
		Executor: func(fn func()) {
			sem <- struct{}{}
			go func() {
				defer func() { <-sem }()
				fn()
			}()
		},
	})
	for k := 1; k <= slots; k++ {
		c.Set(k, 1)
		c.Set(k, 2) // boxes the node, so that the refresh updates it in place
	}
	require.Eventually(t, func() bool { return events.Load() == slots }, time.Second, time.Millisecond)
	clk.Sleep(time.Hour)

	loader := &barrierReloader{}
	loader.arrived.Add(slots)
	for k := 1; k <= slots; k++ {
		_, err := c.Get(context.Background(), k, loader) // stale: starts a refresh
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		v1, _ := c.GetIfPresent(1)
		v2, _ := c.GetIfPresent(2)
		return v1 == 10 && v2 == 20 && events.Load() == 2*slots
	}, 5*time.Second, time.Millisecond, "the refreshes deadlocked on the executor")
}
