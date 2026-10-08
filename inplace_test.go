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
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

func TestCache_InPlaceUpdateDoesNotRetainOldValues(t *testing.T) {
	t.Parallel()

	type blob struct{ data [1 << 10]byte }

	for _, updates := range []int{1, 5} {
		c := Must(&Options[int, *blob]{
			MaximumSize:      1000,
			ExpiryCalculator: ExpiryWriting[int, *blob](time.Hour),
		})

		const n = 100
		var collected atomic.Int64
		track := func() *blob {
			v := &blob{}
			runtime.AddCleanup(v, func(_ int) { collected.Add(1) }, 0)
			return v
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
			v, ok := c.GetIfPresent(i)
			require.True(t, ok)
			require.NotNil(t, v)
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

	// the first update boxed every node, later ones were applied in place
	for i := 0; i < keys; i++ {
		n := c.cache.hashmap.Get(i)
		require.NotNil(t, n)
		require.True(t, n.IsBoxed(), "key %d", i)
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
		c.Set(1, 2) // boxes the node, so that later writes are applied in place
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
	c.Set(1, 0) // boxes the node, so that later writes are applied in place

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
		c.Set(1, 5) // boxes the node

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
		c.Set(1, 100) // boxes the node

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
	c.Set(1, 2) // replaces the node with a boxed one
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
	require.False(t, n.IsBoxed())
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
		c.Set(k, 10) // boxes the node
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
