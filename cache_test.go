// Copyright (c) 2023 Alexey Mayshev and contributors. All rights reserved.
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
	"container/heap"
	"context"
	"fmt"
	"maps"
	"math"
	"math/rand"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/xruntime"
	"github.com/maypok86/otter/v2/stats"
)

func getRandomSize(t *testing.T) int {
	t.Helper()

	const (
		minSize = 10
		maxSize = 1000
	)

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	return r.Intn(maxSize-minSize) + minSize
}

func TestComputeOp_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		op   ComputeOp
		want string
	}{
		{
			op:   CancelOp,
			want: "CancelOp",
		},
		{
			op:   WriteOp,
			want: "WriteOp",
		},
		{
			op:   InvalidateOp,
			want: "InvalidateOp",
		},
		{
			op:   -1,
			want: "<unknown otter.ComputeOp>",
		},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, tt.op.String())
	}
}

func TestMust(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		Must(&Options[int, int]{
			MaximumSize: -1,
		})
	})

	require.NotPanics(t, func() {
		Must[int, int](nil)
	})
}

func TestCache_Unbounded(t *testing.T) {
	t.Parallel()

	statsCounter := stats.NewCounter()
	m := make(map[DeletionCause]int)
	mutex := sync.Mutex{}
	size := getRandomSize(t)
	done := make(chan struct{})
	count := 0
	c := Must[int, int](&Options[int, int]{
		StatsRecorder: statsCounter,
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			count++
			if count == size {
				done <- struct{}{}
			}
			mutex.Unlock()
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}
	for i := 0; i < size; i++ {
		if !c.has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
	}
	for i := size; i < 2*size; i++ {
		if c.has(i) {
			t.Fatalf("the key must not exist: %d", i)
		}
	}

	replaced := size / 2
	for i := 0; i < replaced; i++ {
		c.Set(i, i)
	}
	for i := replaced; i < size; i++ {
		c.Invalidate(i)
	}
	c.CleanUp()
	require.Equal(t, uint64(math.MaxUint64), c.GetMaximum())

	<-done
	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 2 || m[CauseInvalidation] != size-replaced {
		t.Fatalf("cache was supposed to delete %d, but deleted %d entries", size-replaced, m[CauseInvalidation])
	}
	if m[CauseReplacement] != replaced {
		t.Fatalf("cache was supposed to replace %d, but replaced %d entries", replaced, m[CauseReplacement])
	}
	if hitRatio := statsCounter.Snapshot().HitRatio(); hitRatio != 0.5 {
		t.Fatalf("not valid hit ratio. expected %.2f, but got %.2f", 0.5, hitRatio)
	}
}

func TestCache_PinnedWeight(t *testing.T) {
	t.Parallel()

	size := 10
	pinned := 4
	m := make(map[DeletionCause]int)
	mutex := sync.Mutex{}
	fs := &fakeSource{}
	var wg sync.WaitGroup
	c := Must[int, int](&Options[int, int]{
		MaximumWeight: uint64(size),
		Weigher: func(key int, value int) uint32 {
			if key == pinned {
				return 0
			}
			return 1
		},
		Clock:            fs,
		ExpiryCalculator: ExpiryWriting[int, int](2 * time.Second),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
			wg.Done()
		},
	})

	wg.Add(size - 1)
	for i := 0; i < size; i++ {
		c.Set(i, i)
	}
	for i := 0; i < size; i++ {
		if !c.has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
		c.has(i)
	}
	for i := size; i < 2*size; i++ {
		c.Set(i, i)
		if !c.has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
		c.has(i)
	}
	if !c.has(pinned) {
		t.Fatalf("the key must exist: %d", pinned)
	}

	wg.Wait()
	wg.Add(size + 1)
	fs.Sleep(4 * time.Second)
	wg.Wait()

	if c.has(pinned) {
		t.Fatalf("the key must not exist: %d", pinned)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 2 || m[CauseOverflow] != size-1 {
		t.Fatalf("cache was supposed to evict %d, but evicted %d entries", size-1, m[CauseOverflow])
	}
	if m[CauseExpiration] != size+1 {
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", size+1, m[CauseExpiration])
	}
}

func TestCache_SetWithWeight(t *testing.T) {
	t.Parallel()

	statsCounter := stats.NewCounter()
	size := uint64(10)
	c := Must[uint32, int](&Options[uint32, int]{
		MaximumWeight:   size,
		InitialCapacity: 10000,
		Weigher: func(key uint32, value int) uint32 {
			return key
		},
		StatsRecorder: statsCounter,
	})
	c.cache.evictionPolicy.rand = func() uint32 {
		return 1
	}

	goodWeight1 := 1
	goodWeight2 := 2
	badWeight := 8

	c.Set(uint32(goodWeight1), 1)
	c.Set(uint32(goodWeight2), 1)
	c.Set(uint32(badWeight), 1)
	c.CleanUp()
	if !c.has(uint32(goodWeight1)) {
		t.Fatalf("the key must exist: %d", goodWeight1)
	}
	if !c.has(uint32(goodWeight2)) {
		t.Fatalf("the key must exist: %d", goodWeight2)
	}
	if c.has(uint32(badWeight)) {
		t.Fatalf("the key must not exist: %d", badWeight)
	}
}

// liveAfterExpiringOne expires the first entry of the population, if the cache expires entries,
// and returns the entries that are still live. The expired entry stays in the table until
// maintenance removes it.
func liveAfterExpiringOne(cc *cacheContext) map[int]int {
	live := maps.Clone(cc.original)
	if cc.expiry != noExpiry && len(live) > 0 {
		cc.cache.SetExpiresAfter(1, time.Second)
		cc.advance(2 * time.Second)
		delete(live, 1)
	}
	return live
}

// Iteration visits every live entry once, skips an expired one, and stops when the loop body
// breaks.
func TestCache_All(t *testing.T) {
	t.Parallel()

	forEachSpec(t, cacheSpec{population: allPopulations}, func(t *testing.T, cc *cacheContext) {
		live := liveAfterExpiringOne(cc)
		got := make(map[int]int)
		for k, v := range cc.cache.All() {
			require.NotContains(t, got, k, "visited twice")
			got[k] = v
		}
		require.Equal(t, live, got)

		// an iteration that went on after yield returned false would visit again
		visits := 0
		cc.cache.All()(func(int, int) bool {
			visits++
			return false
		})
		require.Equal(t, min(1, len(live)), visits)
	})
}

func TestCache_Keys(t *testing.T) {
	t.Parallel()

	forEachSpec(t, cacheSpec{population: allPopulations}, func(t *testing.T, cc *cacheContext) {
		live := liveAfterExpiringOne(cc)
		require.ElementsMatch(t, slices.Collect(maps.Keys(live)), slices.Collect(cc.cache.Keys()))

		visits := 0
		cc.cache.Keys()(func(int) bool {
			visits++
			return false
		})
		require.Equal(t, min(1, len(live)), visits)
	})
}

func TestCache_Values(t *testing.T) {
	t.Parallel()

	forEachSpec(t, cacheSpec{population: allPopulations}, func(t *testing.T, cc *cacheContext) {
		live := liveAfterExpiringOne(cc)
		require.ElementsMatch(t, slices.Collect(maps.Values(live)), slices.Collect(cc.cache.Values()))

		visits := 0
		cc.cache.Values()(func(int) bool {
			visits++
			return false
		})
		require.Equal(t, min(1, len(live)), visits)
	})
}

func gcHelper(t *testing.T) *atomic.Bool {
	t.Helper()

	size := 10
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		ExpiryCalculator: ExpiryWriting[int, int](time.Hour),
	})

	var cleaned atomic.Bool
	runtime.AddCleanup(c.cache, func(c *atomic.Bool) {
		c.Store(true)
	}, &cleaned)

	for i := 0; i < size; i++ {
		c.Set(i, i)
		c.has(i)
	}

	return &cleaned
}

func TestCache_GC(t *testing.T) {
	t.Parallel()

	cleaned := gcHelper(t)

	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	runtime.GC()

	if !cleaned.Load() {
		t.Fatal("cache should be collected")
	}
}

// A callback that the cache keeps may hold the cache itself, as an OnDeletion that writes to it
// does. The periodic clean-up goroutine must not keep it reachable through such a callback: the
// cache is still collected, and the goroutine returns at its next tick.
func TestCache_GCWithCallbackHoldingTheCache(t *testing.T) {
	t.Parallel()

	for _, expiry := range []bool{false, true} {
		t.Run(fmt.Sprintf("expiry=%v", expiry), func(t *testing.T) {
			t.Parallel()

			var done chan struct{}
			cleaned := func() *atomic.Bool {
				var c *Cache[int, int]
				opts := &Options[int, int]{
					MaximumSize: 10,
					OnDeletion: func(e DeletionEvent[int, int]) {
						c.Set(e.Key+1, e.Value)
					},
				}
				if expiry {
					opts.ExpiryCalculator = ExpiryWriting[int, int](time.Hour)
				}
				c = Must(opts)
				c.Set(1, 1)
				done = c.cache.done

				var cleaned atomic.Bool
				runtime.AddCleanup(c.cache, func(c *atomic.Bool) {
					c.Store(true)
				}, &cleaned)
				return &cleaned
			}()

			for i := 0; !cleaned.Load(); i++ {
				require.Less(t, i, 10, "the cache was not collected")
				runtime.GC()
				time.Sleep(50 * time.Millisecond)
			}
			if expiry {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("the periodic clean-up goroutine did not return")
				}
			}
		})
	}
}

func TestCache_CleanUp(t *testing.T) {
	t.Parallel()

	size := 10
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		ExpiryCalculator: ExpiryCreating[int, int](2 * time.Second),
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	for i := 0; i < size; i++ {
		c.has(i)
		c.has(i)
	}

	if l := c.cache.readBuffer.Len(); l == 0 {
		t.Fatalf("stripedBufferLen = %d, want > %d", l, 0)
	}
	c.CleanUp()

	if cacheSize := c.EstimatedSize(); cacheSize != size {
		t.Fatalf("c.EstimatedSize() = %d, want = %d", cacheSize, size)
	}
	if l := c.cache.writeBuffer.Size(); l != 0 {
		t.Fatalf("writeBufferLen = %d, want = %d", l, 0)
	}
	if l := c.cache.readBuffer.Len(); l != 0 {
		t.Fatalf("stripedBufferLen = %d, want = %d", l, 0)
	}
}

func TestCache_InvalidateAll(t *testing.T) {
	t.Parallel()

	forEachSpec(t, cacheSpec{population: allPopulations, executor: allExecutors}, func(t *testing.T, cc *cacheContext) {
		require.Equal(t, len(cc.original), cc.cache.EstimatedSize())

		cc.cache.InvalidateAll()
		require.Zero(t, cc.cache.EstimatedSize())
		events := make([]DeletionEvent[int, int], 0, len(cc.original))
		for _, key := range cc.keys() {
			cc.requireValue(t, key, 0)
			events = append(events, DeletionEvent[int, int]{Key: key, Value: cc.original[key], Cause: CauseInvalidation})
		}
		cc.run()
		cc.requireDeletionsByKey(t, events...)
	})
}

// InvalidateAll reports the entries that have expired as expired, as every other removal of an
// expired entry does.
func TestCache_InvalidateAllExpired(t *testing.T) {
	t.Parallel()

	spec := cacheSpec{expiry: withExpiry, population: []populationKind{partialCache, fullCache}, executor: allExecutors}
	forEachSpec(t, spec, func(t *testing.T, cc *cacheContext) {
		cc.advance(2 * specExpiry)
		cc.cache.InvalidateAll()
		require.Zero(t, cc.cache.EstimatedSize())
		events := make([]DeletionEvent[int, int], 0, len(cc.original))
		for _, key := range cc.keys() {
			events = append(events, DeletionEvent[int, int]{Key: key, Value: cc.original[key], Cause: CauseExpiration})
		}
		cc.run()
		cc.requireDeletionsByKey(t, events...)
	})
}

// An update of every entry replaces each value once, and concurrent reads find every new value.
func TestCache_Set(t *testing.T) {
	t.Parallel()

	spec := cacheSpec{population: []populationKind{partialCache, fullCache}}
	forEachSpec(t, spec, func(t *testing.T, cc *cacheContext) {
		keys := cc.keys()
		events := make([]DeletionEvent[int, int], 0, len(keys))
		for _, key := range keys {
			cc.cache.Set(key, key)
			events = append(events, DeletionEvent[int, int]{Key: key, Value: cc.original[key], Cause: CauseReplacement})
		}

		var (
			wg   sync.WaitGroup
			errs = make(chan error, xruntime.Parallelism())
		)
		for i := 0; i < int(xruntime.Parallelism()); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				r := rand.New(rand.NewSource(time.Now().UnixNano()))
				for a := 0; a < 1000; a++ {
					key := keys[r.Intn(len(keys))]
					if v, ok := cc.cache.GetIfPresent(key); !ok || v != key {
						errs <- fmt.Errorf("key %d: got %d, %v", key, v, ok)
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		require.NoError(t, <-errs)
		require.Equal(t, 1.0, cc.stats.Snapshot().HitRatio())

		cc.run()
		cc.requireDeletions(t, events...)
	})
}

// SetIfAbsent keeps the value of a present key, and neither it nor its write of an absent key is
// a miss.
func TestCache_SetIfAbsent(t *testing.T) {
	t.Parallel()

	spec := cacheSpec{population: withRoom}
	forEachSpec(t, spec, func(t *testing.T, cc *cacheContext) {
		for _, key := range cc.keys() {
			v, ok := cc.cache.SetIfAbsent(key, key)
			require.False(t, ok, "set wasn't dropped: %d", key)
			require.Equal(t, cc.original[key], v)
			cc.requireValue(t, key, cc.original[key])
		}
		v, ok := cc.cache.SetIfAbsent(absentKey, 7)
		require.True(t, ok)
		require.Equal(t, 7, v)
		cc.requireValue(t, absentKey, 7)
		require.Zero(t, cc.stats.Snapshot().Misses)
		cc.requireDeletions(t)
	})
}

func TestCache_ComputeIfAbsent(t *testing.T) {
	t.Parallel()

	t.Run("functionCalledOnce", func(t *testing.T) {
		t.Parallel()

		const iters = 100
		c := Must(&Options[int, int]{})
		for i := 0; i < iters; i++ {
			actualValue, ok := c.ComputeIfAbsent(i, func() (newValue int, cancel bool) {
				newValue, i = i, i+1
				return newValue, false
			})
			require.True(t, ok)
			require.Equal(t, i-1, actualValue)
		}
		require.Equal(t, iters/2, c.EstimatedSize())
		for k, v := range c.All() {
			require.Equal(t, k, v)
		}
	})
	t.Run("general", func(t *testing.T) {
		t.Parallel()

		// a few absent keys fit in the cache without evictions
		absent := []int{absentKey, absentKey + 1, absentKey + 2}
		forEachSpec(t, cacheSpec{population: withRoom}, func(t *testing.T, cc *cacheContext) {
			for _, key := range absent {
				v, ok := cc.cache.ComputeIfAbsent(key, func() (int, bool) { return key, true })
				require.False(t, ok)
				require.Equal(t, 0, v)
			}
			require.Equal(t, len(cc.original), cc.cache.EstimatedSize())

			for _, key := range absent {
				v, ok := cc.cache.ComputeIfAbsent(key, func() (int, bool) { return key, false })
				require.True(t, ok)
				require.Equal(t, key, v)
			}
			for _, key := range append(cc.keys(), absent...) {
				want, _ := cc.cache.GetIfPresent(key)
				v, ok := cc.cache.ComputeIfAbsent(key, func() (int, bool) { return key + 1, false })
				require.True(t, ok)
				require.Equal(t, want, v)
			}
			snapshot := cc.stats.Snapshot()
			// each present key is a hit twice: by ComputeIfAbsent and by the read before it
			require.Equal(t, uint64(2*(len(cc.original)+len(absent))), snapshot.Hits)
			require.Equal(t, uint64(2*len(absent)), snapshot.Misses)
			cc.requireDeletions(t)
		})
	})
	t.Run("failedRead", func(t *testing.T) {
		t.Parallel()

		counter := stats.NewCounter()
		deletions := uint64(0)
		c := Must(&Options[int, int]{
			StatsRecorder: counter,
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				deletions++
			},
		})
		key := 15

		start := make(chan struct{})

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()

			<-start
			v, ok := c.ComputeIfAbsent(key, func() (newValue int, cancel bool) {
				panic("incorrect call")
			})
			require.True(t, ok)
			require.Equal(t, key+1, v)
		}()

		v, ok := c.Compute(key, func(oldValue int, found bool) (newValue int, op ComputeOp) {
			start <- struct{}{}
			return key + 1, WriteOp
		})
		require.True(t, ok)
		require.Equal(t, key+1, v)

		wg.Wait()

		snapshot := counter.Snapshot()
		require.Contains(t, []uint64{0, 1}, snapshot.Hits)
		require.Contains(t, []uint64{1, 2}, snapshot.Misses)
		require.Equal(t, uint64(0), deletions)
	})
}

func TestCache_ComputeIfPresent(t *testing.T) {
	t.Parallel()

	t.Run("general", func(t *testing.T) {
		t.Parallel()

		forEachSpec(t, cacheSpec{population: withRoom}, func(t *testing.T, cc *cacheContext) {
			key, other := absentKey, absentKey+1

			// Store a new value.
			v, ok := cc.cache.Compute(key, func(oldValue int, found bool) (int, ComputeOp) {
				require.Equal(t, 0, oldValue)
				require.False(t, found)
				return 42, WriteOp
			})
			require.True(t, ok)
			require.Equal(t, 42, v)

			// Update an existing value.
			v, ok = cc.cache.ComputeIfPresent(key, func(oldValue int) (int, ComputeOp) {
				require.Equal(t, 42, oldValue)
				return oldValue + 42, WriteOp
			})
			require.True(t, ok)
			require.Equal(t, 84, v)

			// noop
			v, ok = cc.cache.ComputeIfPresent(key, func(oldValue int) (int, ComputeOp) {
				require.Equal(t, 84, oldValue)
				return 1, CancelOp
			})
			require.True(t, ok)
			require.Equal(t, 84, v)

			// noop
			v, ok = cc.cache.ComputeIfPresent(other, func(oldValue int) (int, ComputeOp) {
				panic("incorrect call")
			})
			require.False(t, ok)
			require.Equal(t, 0, v)

			// Delete an existing value.
			v, ok = cc.cache.ComputeIfPresent(key, func(oldValue int) (int, ComputeOp) {
				require.Equal(t, 84, oldValue)
				return 57, InvalidateOp
			})
			require.False(t, ok)
			require.Equal(t, 0, v)

			snapshot := cc.stats.Snapshot()
			require.Equal(t, uint64(3), snapshot.Hits)
			require.Equal(t, uint64(2), snapshot.Misses)
			cc.run()
			cc.requireDeletions(t,
				DeletionEvent[int, int]{Key: key, Value: 42, Cause: CauseReplacement},
				DeletionEvent[int, int]{Key: key, Value: 84, Cause: CauseInvalidation},
			)
		})
	})
	t.Run("failedRead", func(t *testing.T) {
		t.Parallel()

		counter := stats.NewCounter()
		deletions := uint64(0)
		c := Must(&Options[int, int]{
			StatsRecorder: counter,
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				deletions++
			},
		})
		key := 15

		start := make(chan struct{})

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()

			<-start
			v, ok := c.ComputeIfPresent(key, func(oldValue int) (newValue int, op ComputeOp) {
				panic("incorrect call")
			})
			require.False(t, ok)
			require.Equal(t, 0, v)
		}()

		c.Set(key, key)

		v, ok := c.Compute(key, func(oldValue int, found bool) (newValue int, op ComputeOp) {
			start <- struct{}{}
			return 0, InvalidateOp
		})
		require.False(t, ok)
		require.Equal(t, 0, v)

		wg.Wait()

		snapshot := counter.Snapshot()
		require.Contains(t, []uint64{1, 2}, snapshot.Hits)
		require.Contains(t, []uint64{0, 1}, snapshot.Misses)
		require.Equal(t, uint64(1), deletions)
	})
	t.Run("notARead", func(t *testing.T) {
		t.Parallel()

		counter := stats.NewCounter()
		clk := newNonTickingClock()
		c := Must(&Options[int, int]{
			MaximumSize:      100,
			StatsRecorder:    counter,
			Clock:            clk,
			ExpiryCalculator: ExpiryAccessing[int, int](10 * time.Second),
		})
		c.Set(1, 1)
		c.Set(2, 2)
		clk.Sleep(5 * time.Second)

		// Like Compute, ComputeIfPresent does not read the entry before calling the function: a
		// cancelled call keeps the deadline, and a panicking one records nothing.
		c.ComputeIfPresent(1, func(oldValue int) (int, ComputeOp) {
			return oldValue, CancelOp
		})
		c.Compute(2, func(oldValue int, found bool) (int, ComputeOp) {
			return oldValue, CancelOp
		})
		e1, ok := c.GetEntryQuietly(1)
		require.True(t, ok)
		e2, ok := c.GetEntryQuietly(2)
		require.True(t, ok)
		require.Equal(t, 5*time.Second, e1.ExpiresAfter())
		require.Equal(t, e2.ExpiresAfter(), e1.ExpiresAfter())
		require.Equal(t, uint64(2), counter.Snapshot().Hits)

		require.Panics(t, func() {
			c.ComputeIfPresent(1, func(oldValue int) (int, ComputeOp) {
				panic("remapping failed")
			})
		})
		e1, ok = c.GetEntryQuietly(1)
		require.True(t, ok)
		require.Equal(t, 5*time.Second, e1.ExpiresAfter())
		require.Equal(t, uint64(2), counter.Snapshot().Hits)
	})
}

func TestCache_Compute(t *testing.T) {
	t.Parallel()

	t.Run("general", func(t *testing.T) {
		t.Parallel()

		forEachSpec(t, cacheSpec{population: withRoom}, func(t *testing.T, cc *cacheContext) {
			key, other := absentKey, absentKey+1

			// Store a new value.
			v, ok := cc.cache.Compute(key, func(oldValue int, found bool) (int, ComputeOp) {
				require.Equal(t, 0, oldValue)
				require.False(t, found)
				return 42, WriteOp
			})
			require.True(t, ok)
			require.Equal(t, 42, v)

			// Update an existing value.
			v, ok = cc.cache.Compute(key, func(oldValue int, found bool) (int, ComputeOp) {
				require.Equal(t, 42, oldValue)
				require.True(t, found)
				return oldValue + 42, WriteOp
			})
			require.True(t, ok)
			require.Equal(t, 84, v)

			// noop
			v, ok = cc.cache.Compute(key, func(oldValue int, found bool) (int, ComputeOp) {
				require.Equal(t, 84, oldValue)
				require.True(t, found)
				return 1, CancelOp
			})
			require.True(t, ok)
			require.Equal(t, 84, v)

			// Delete an existing value.
			v, ok = cc.cache.Compute(key, func(oldValue int, found bool) (int, ComputeOp) {
				require.Equal(t, 84, oldValue)
				require.True(t, found)
				return 0, InvalidateOp
			})
			require.False(t, ok)
			require.Equal(t, 0, v)

			// Try to delete an absent key: the returned value is ignored.
			v, ok = cc.cache.Compute(other, func(oldValue int, found bool) (int, ComputeOp) {
				require.Equal(t, 0, oldValue)
				require.False(t, found)
				return 42, InvalidateOp
			})
			require.False(t, ok)
			require.Equal(t, 0, v)

			snapshot := cc.stats.Snapshot()
			require.Equal(t, uint64(3), snapshot.Hits)
			require.Equal(t, uint64(2), snapshot.Misses)
			cc.run()
			cc.requireDeletions(t,
				DeletionEvent[int, int]{Key: key, Value: 42, Cause: CauseReplacement},
				DeletionEvent[int, int]{Key: key, Value: 84, Cause: CauseInvalidation},
			)
		})
	})
	t.Run("panic", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{})

		require.Panics(t, func() {
			c.Compute(0, func(oldValue int, found bool) (newValue int, op ComputeOp) {
				panic("olololololo")
			})
		})
		_, ok := c.GetIfPresent(0)
		require.False(t, ok)
	})
	t.Run("incorrectComputeOp", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{})

		require.Panics(t, func() {
			c.Compute(0, func(oldValue int, found bool) (newValue int, op ComputeOp) {
				return -100, -1
			})
		})
		_, ok := c.GetIfPresent(0)
		require.False(t, ok)
	})
}

func TestCache_SetWithExpiresAt(t *testing.T) {
	t.Parallel()

	size := getRandomSize(t)
	var (
		mutex sync.Mutex
		wg    sync.WaitGroup
	)
	m := make(map[DeletionCause]int)
	statsCounter := stats.NewCounter()
	fs := &fakeSource{}
	wg.Add(size)
	c := Must(&Options[int, int]{
		InitialCapacity:  size,
		StatsRecorder:    statsCounter,
		Clock:            fs,
		ExpiryCalculator: ExpiryCreating[int, int](time.Hour),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
			wg.Done()
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	fs.Sleep(time.Hour + 2*time.Second)
	for i := 0; i < size; i++ {
		if c.has(i) {
			t.Fatalf("key should be expired: %d", i)
		}
	}

	c.CleanUp()

	if cacheSize := c.EstimatedSize(); cacheSize != 0 {
		t.Fatalf("c.EstimatedSize() = %d, want = %d", cacheSize, 0)
	}

	wg.Wait()
	mutex.Lock()
	if e := m[CauseExpiration]; len(m) != 1 || e != size {
		mutex.Unlock()
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", size, e)
	}
	if statsCounter.Snapshot().Evictions != uint64(m[CauseExpiration]) {
		mutex.Unlock()
		t.Fatalf(
			"Eviction statistics are not collected for expiration. Evictions: %d, expired entries: %d",
			statsCounter.Snapshot().Evictions,
			m[CauseExpiration],
		)
	}
	mutex.Unlock()

	m = make(map[DeletionCause]int)
	statsCounter = stats.NewCounter()
	if size%2 == 1 {
		size++
	}
	fs = &fakeSource{}
	wg.Add(size + size/2)
	cc := Must(&Options[int, int]{
		MaximumSize:   size,
		StatsRecorder: statsCounter,
		Clock:         fs,
		ExpiryCalculator: ExpiryWritingFunc(func(entry Entry[int, int]) time.Duration {
			if entry.Key%2 == 0 {
				return time.Hour
			}
			return 4 * time.Hour
		}),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
			wg.Done()
		},
	})

	for i := 0; i < size; i++ {
		cc.Set(i, i)
	}

	fs.Sleep(time.Hour + time.Second)

	if c.EstimatedSize() != size%2 {
		t.Fatalf("half of the keys must be expired. wantedCurrentSize %d, got %d", size%2, c.EstimatedSize())
	}

	for i := 0; i < size; i++ {
		if i%2 == 0 {
			continue
		}
		cc.Set(i, i)
	}

	fs.Sleep(3*time.Hour + time.Second)

	for i := 0; i < size; i++ {
		if i%2 == 0 {
			continue
		}
		if !cc.has(i) {
			t.Fatalf("key should not be expired: %d", i)
		}
	}

	fs.Sleep(time.Hour + time.Second)

	for i := 0; i < size; i++ {
		if cc.has(i) {
			t.Fatalf("key should be expired: %d", i)
		}
	}

	cc.CleanUp()

	if cacheSize := cc.EstimatedSize(); cacheSize != 0 {
		t.Fatalf("c.EstimatedSize() = %d, want = %d", cacheSize, 0)
	}
	if misses := statsCounter.Snapshot().Misses; misses != uint64(size) {
		t.Fatalf("c.Stats().Misses = %d, want = %d", misses, size)
	}
	wg.Wait()
	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 2 || m[CauseExpiration] != size && m[CauseReplacement] != size/2 {
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", size, m[CauseExpiration])
	}
	if statsCounter.Snapshot().Evictions != uint64(m[CauseExpiration]) {
		mutex.Unlock()
		t.Fatalf(
			"Eviction statistics are not collected for expiration. Evictions: %d, expired entries: %d",
			statsCounter.Snapshot().Evictions,
			m[CauseExpiration],
		)
	}
}

func TestCache_SetWithExpiresAfterAccessing(t *testing.T) {
	t.Parallel()

	size := getRandomSize(t)
	var (
		mutex sync.Mutex
		wg    sync.WaitGroup
	)
	m := make(map[DeletionCause]int)
	statsCounter := stats.NewCounter()
	fs := &fakeSource{}
	wg.Add(size + size/2)
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		InitialCapacity:  size,
		StatsRecorder:    statsCounter,
		Clock:            fs,
		ExpiryCalculator: ExpiryAccessing[int, int](2 * time.Hour),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
			wg.Done()
		},
	})
	c.cache.evictionPolicy.rand = func() uint32 {
		return 1
	}

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	c.CleanUp()
	fs.Sleep(2*time.Hour + 1*time.Second)
	for i := 0; i < size; i++ {
		if c.has(i) {
			t.Fatalf("key should be expired: %d", i)
		}
	}

	c.CleanUp()

	if cacheSize := c.EstimatedSize(); cacheSize != 0 {
		t.Fatalf("cacheSize = %d, want = %d", cacheSize, 0)
	}

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	fs.Sleep(time.Hour)

	for i := 0; i < size; i++ {
		if i%2 == 0 {
			if !c.has(i) {
				t.Fatalf("key should be expired: %d", i)
			}
		} else {
			c.Set(i, i)
		}
	}

	// past the deadlines of the second round of writes, but before the extended ones
	fs.Sleep(2*time.Hour - time.Second)
	c.CleanUp()

	if cacheSize := c.EstimatedSize(); cacheSize == 0 {
		t.Fatal("cacheSize should be positive")
	}
	if misses := statsCounter.Snapshot().Misses; misses != uint64(size) {
		t.Fatalf("c.Stats().Misses = %d, want = %d", misses, size)
	}
	wg.Wait()
	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 2 || m[CauseExpiration] != size && m[CauseReplacement] != size/2 {
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", size, m[CauseExpiration])
	}
	if statsCounter.Snapshot().Evictions != uint64(m[CauseExpiration]) {
		mutex.Unlock()
		t.Fatalf(
			"Eviction statistics are not collected for expiration. Evictions: %d, expired entries: %d",
			statsCounter.Snapshot().Evictions,
			m[CauseExpiration],
		)
	}
}

func TestCache_ExpiredEntryRemovedByWriteIsCountedAsEviction(t *testing.T) {
	t.Parallel()

	for name, remove := range map[string]func(c *Cache[int, uint32]){
		"Set":           func(c *Cache[int, uint32]) { c.Set(1, 5) },
		"Invalidate":    func(c *Cache[int, uint32]) { c.Invalidate(1) },
		"InvalidateAll": func(c *Cache[int, uint32]) { c.InvalidateAll() },
		"Compute": func(c *Cache[int, uint32]) {
			c.Compute(1, func(uint32, bool) (uint32, ComputeOp) { return 0, CancelOp })
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			clk := newNonTickingClock()
			counter := stats.NewCounter()
			c := Must(&Options[int, uint32]{
				MaximumWeight:    100,
				Weigher:          func(_ int, v uint32) uint32 { return v },
				Clock:            clk,
				StatsRecorder:    counter,
				ExpiryCalculator: ExpiryWriting[int, uint32](time.Minute),
				Executor:         func(fn func()) { fn() },
			})
			c.Set(1, 3)
			clk.Sleep(2 * time.Minute)
			remove(c)

			s := counter.Snapshot()
			require.Equal(t, uint64(1), s.Evictions)
			require.Equal(t, uint64(3), s.EvictionWeight)
		})
	}
}

func TestCache_EvictionStatsMatchDeletionListener(t *testing.T) {
	t.Parallel()

	seed := time.Now().UnixNano()
	r := rand.New(rand.NewSource(seed))
	clk := newNonTickingClock()
	counter := stats.NewCounter()
	var evictions, evictionWeight uint64
	c := Must(&Options[int, uint32]{
		MaximumWeight:     20,
		Weigher:           func(_ int, v uint32) uint32 { return v },
		Clock:             clk,
		StatsRecorder:     counter,
		ExpiryCalculator:  ExpiryWriting[int, uint32](time.Minute),
		RefreshCalculator: RefreshWriting[int, uint32](30 * time.Second),
		OnDeletion: func(e DeletionEvent[int, uint32]) {
			if e.WasEvicted() {
				evictions++
				evictionWeight += uint64(e.Value)
			}
		},
		Executor: func(fn func()) { fn() },
	})
	loader := LoaderFunc[int, uint32](func(context.Context, int) (uint32, error) {
		return uint32(r.Intn(5)), nil
	})
	bulkLoader := BulkLoaderFunc[int, uint32](func(_ context.Context, keys []int) (map[int]uint32, error) {
		m := make(map[int]uint32, len(keys))
		for _, k := range keys {
			m[k] = uint32(r.Intn(5))
		}
		return m, nil
	})

	for range 10_000 {
		k, v := r.Intn(8), uint32(r.Intn(5))
		switch r.Intn(11) {
		case 0:
			c.Set(k, v)
		case 1:
			c.SetIfAbsent(k, v)
		case 2:
			c.Invalidate(k)
		case 3:
			c.Compute(k, func(uint32, bool) (uint32, ComputeOp) { return v, ComputeOp(r.Intn(3)) })
		case 4:
			_, _ = c.Get(context.Background(), k, loader)
		case 5:
			_, _ = c.BulkGet(context.Background(), []int{k, r.Intn(8)}, bulkLoader)
		case 6:
			c.Refresh(context.Background(), k, loader)
		case 7:
			c.SetExpiresAfter(k, time.Duration(1+r.Intn(120))*time.Second)
		case 8:
			if r.Intn(50) == 0 {
				c.InvalidateAll()
			}
		default:
			clk.Sleep(time.Duration(r.Intn(40)) * time.Second)
		}
	}
	c.CleanUp()

	s := counter.Snapshot()
	require.Equal(t, evictions, s.Evictions, "seed %d", seed)
	require.Equal(t, evictionWeight, s.EvictionWeight, "seed %d", seed)
}

func TestCache_Invalidate(t *testing.T) {
	t.Parallel()

	forEachSpec(t, cacheSpec{population: allPopulations}, func(t *testing.T, cc *cacheContext) {
		events := make([]DeletionEvent[int, int], 0, len(cc.original))
		for _, key := range cc.keys() {
			v, ok := cc.cache.Invalidate(key)
			require.True(t, ok)
			require.Equal(t, cc.original[key], v)
			cc.requireValue(t, key, 0)
			events = append(events, DeletionEvent[int, int]{Key: key, Value: cc.original[key], Cause: CauseInvalidation})
		}
		v, ok := cc.cache.Invalidate(absentKey)
		require.False(t, ok)
		require.Zero(t, v)

		cc.run()
		cc.requireDeletions(t, events...)
	})
}

func TestCache_ConcurrentInvalidateAll(t *testing.T) {
	t.Parallel()

	c := Must(&Options[string, string]{
		MaximumSize:      1000,
		ExpiryCalculator: ExpiryWriting[string, string](time.Hour),
	})

	done := make(chan struct{})
	go func() {
		const (
			goroutines = 10
			iterations = 5
		)

		var wg sync.WaitGroup
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func() {
				for j := 0; j < iterations; j++ {
					c.InvalidateAll()
				}
				wg.Done()
			}()
		}

		wg.Wait()
		done <- struct{}{}
	}()

	<-done
}

func TestCache_IsWeighted(t *testing.T) {
	t.Parallel()

	cache := Must(&Options[int, int]{
		MaximumSize: 1000,
	})

	require.False(t, cache.IsWeighted())

	cache = Must(&Options[int, int]{
		MaximumWeight: 1000,
		Weigher: func(key int, value int) uint32 {
			return uint32(value)
		},
	})

	require.True(t, cache.IsWeighted())
}

func TestCache_IsRecordingStats(t *testing.T) {
	t.Parallel()

	cache := Must(&Options[int, int]{
		StatsRecorder: stats.NewCounter(),
	})

	require.True(t, cache.IsRecordingStats())

	cache = Must(&Options[int, int]{
		StatsRecorder: &stats.NoopRecorder{},
	})

	require.False(t, cache.IsRecordingStats())
}

type fakeRecorder struct {
	hits   atomic.Uint64
	misses atomic.Uint64
}

func (f *fakeRecorder) RecordHits(count int) {
	f.hits.Add(uint64(count))
}

func (f *fakeRecorder) RecordMisses(count int) {
	f.misses.Add(uint64(count))
}

func (f *fakeRecorder) RecordEviction(weight uint32) {
	panic("implement me")
}

func (f *fakeRecorder) RecordLoadSuccess(loadTime time.Duration) {
	panic("implement me")
}

func (f *fakeRecorder) RecordLoadFailure(loadTime time.Duration) {
	panic("implement me")
}

func TestCache_Stats(t *testing.T) {
	t.Parallel()

	counter := stats.NewCounter()
	cache := Must(&Options[int, int]{
		StatsRecorder: counter,
	})

	for i := 0; i < 100; i++ {
		cache.Set(i, i)
		cache.GetIfPresent(i)
	}

	snapshot := counter.Snapshot()
	require.Equal(t, uint64(100), snapshot.Hits)
	require.Equal(t, uint64(0), snapshot.Misses)
	require.Equal(t, snapshot, cache.Stats())

	fr := &fakeRecorder{}
	cache = Must(&Options[int, int]{
		StatsRecorder: fr,
	})

	for i := 0; i < 100; i++ {
		cache.Set(i, i)
		cache.GetIfPresent(i)
	}

	require.Equal(t, uint64(100), fr.hits.Load())
	require.Equal(t, uint64(0), fr.misses.Load())
	require.Equal(t, stats.Stats{}, cache.Stats())
}

func TestCache_Ratio(t *testing.T) {
	t.Parallel()

	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	statsCounter := stats.NewCounter()
	capacity := 100
	c := Must(&Options[uint64, uint64]{
		MaximumSize:   capacity,
		StatsRecorder: statsCounter,
		Executor: func(fn func()) {
			fn()
		},
		OnDeletion: func(e DeletionEvent[uint64, uint64]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		},
	})

	z := rand.NewZipf(rand.New(rand.NewSource(time.Now().UnixNano())), 1.0001, 1, 1000)

	o := newOptimal(100)
	for i := 0; i < 10000; i++ {
		k := z.Uint64()

		o.Get(k)
		if !c.has(k) {
			c.Set(k, k)
		}
	}

	t.Logf("actual size: %d, capacity: %d", c.EstimatedSize(), capacity)
	t.Logf("actual: %.2f, optimal: %.2f", statsCounter.Snapshot().HitRatio(), o.Ratio())

	if size := c.EstimatedSize(); size != capacity {
		t.Fatalf("not valid cache size. expected %d, but got %d", capacity, size)
	}

	mutex.Lock()
	defer mutex.Unlock()
	t.Logf("evicted: %d", m[CauseOverflow])
	if len(m) != 1 || m[CauseOverflow] <= 0 || m[CauseOverflow] > 5000 {
		t.Fatalf("cache was supposed to evict positive number of entries, but evicted %d entries", m[CauseOverflow])
	}
}

func TestCache_ValidateFunctions(t *testing.T) {
	t.Parallel()

	getPublicMethods := func(v any) map[string]bool {
		tp := reflect.TypeOf(v)

		res := make(map[string]bool, tp.NumMethod())
		for i := 0; i < tp.NumMethod(); i++ {
			method := tp.Method(i)
			res[method.Name] = true
		}
		return res
	}

	publicMethods := getPublicMethods(&Cache[int, int]{})
	implPublicMethods := getPublicMethods(&cache[int, int]{})

	for m := range publicMethods {
		if implPublicMethods[m] {
			continue
		}
		t.Errorf("Cache has an unknown %s method", m)
	}
	for m := range implPublicMethods {
		if publicMethods[m] {
			continue
		}
		t.Errorf("cache has an unknown %s method", m)
	}
}

type optimal struct {
	capacity uint64
	hits     map[uint64]uint64
	access   []uint64
}

func newOptimal(capacity uint64) *optimal {
	return &optimal{
		capacity: capacity,
		hits:     make(map[uint64]uint64),
		access:   make([]uint64, 0),
	}
}

func (o *optimal) Get(key uint64) {
	o.hits[key]++
	o.access = append(o.access, key)
}

func (o *optimal) Ratio() float64 {
	look := make(map[uint64]struct{}, o.capacity)
	data := &optimalHeap{}
	heap.Init(data)
	hits := 0
	misses := 0
	for _, key := range o.access {
		if _, has := look[key]; has {
			hits++
			continue
		}
		if uint64(data.Len()) >= o.capacity {
			victim := heap.Pop(data)
			delete(look, victim.(*optimalItem).key)
		}
		misses++
		look[key] = struct{}{}
		heap.Push(data, &optimalItem{key, o.hits[key]})
	}
	if hits == 0 && misses == 0 {
		return 0.0
	}
	return float64(hits) / float64(hits+misses)
}

type optimalItem struct {
	key  uint64
	hits uint64
}

type optimalHeap []*optimalItem

func (h optimalHeap) Len() int           { return len(h) }
func (h optimalHeap) Less(i, j int) bool { return h[i].hits < h[j].hits }
func (h optimalHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *optimalHeap) Push(x any) {
	*h = append(*h, x.(*optimalItem))
}

func (h *optimalHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

func TestCache_IterationVisitsKeysReplacedDuringIt(t *testing.T) {
	t.Parallel()

	// A string is kept in its node until the entry is updated, so the first update of an entry
	// replaces its node. A key replaced while it is iterated over is present before and after the
	// replacement: the iteration visits it, exactly once.
	const keys = 100
	expiring := func() *Cache[int, string] {
		return Must(&Options[int, string]{ExpiryCalculator: ExpiryWriting[int, string](time.Hour)})
	}
	bounded := func() *Cache[int, string] {
		return Must(&Options[int, string]{MaximumSize: 1000})
	}
	entryKeys := func(seq func(*Cache[int, string]) func(func(Entry[int, string]) bool)) func(*Cache[int, string]) func(func(int) bool) {
		return func(c *Cache[int, string]) func(func(int) bool) {
			return func(yield func(int) bool) {
				for e := range seq(c) {
					if !yield(e.Key) {
						return
					}
				}
			}
		}
	}
	tests := []struct {
		name  string
		cache func() *Cache[int, string]
		keys  func(c *Cache[int, string]) func(func(int) bool)
	}{
		{name: "All", cache: expiring, keys: func(c *Cache[int, string]) func(func(int) bool) {
			return func(yield func(int) bool) {
				for k := range c.All() {
					if !yield(k) {
						return
					}
				}
			}
		}},
		{name: "Keys", cache: expiring, keys: func(c *Cache[int, string]) func(func(int) bool) { return c.Keys() }},
		{name: "Hottest", cache: bounded, keys: entryKeys(func(c *Cache[int, string]) func(func(Entry[int, string]) bool) {
			return c.Hottest()
		})},
		{name: "Coldest", cache: bounded, keys: entryKeys(func(c *Cache[int, string]) func(func(Entry[int, string]) bool) {
			return c.Coldest()
		})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The keys replaced after their bucket was copied are the other keys of the first
			// bucket: with about three keys per bucket, several rounds make sure there are some.
			for round := range 10 {
				c := tt.cache()
				for k := range keys {
					c.Set(k, "a")
				}
				seen := make(map[int]int, keys)
				for k := range tt.keys(c) {
					if len(seen) == 0 {
						for k := range keys {
							c.Set(k, "b")
						}
					}
					seen[k]++
				}
				require.Len(t, seen, keys, "round %d", round)
				for k, n := range seen {
					require.Equal(t, 1, n, "round %d, key %d", round, k)
				}
			}
		})
	}
}

func TestCache_GetEntryQuietlyDuringReplacement(t *testing.T) {
	t.Parallel()

	// OnAtomicDeletion runs while a write replaces the node: the key still holds the old value.
	var (
		c     *Cache[int, string]
		got   string
		found bool
	)
	c = Must(&Options[int, string]{
		ExpiryCalculator: ExpiryWriting[int, string](time.Hour),
		OnAtomicDeletion: func(e DeletionEvent[int, string]) {
			if e.Cause == CauseReplacement {
				var entry Entry[int, string]
				entry, found = c.GetEntryQuietly(1)
				got = entry.Value
			}
		},
	})
	c.Set(1, "a")
	c.Set(1, "b")
	require.True(t, found)
	require.Equal(t, "a", got)
}

// A cache without expiration has no periodic clean-up goroutine, so StopAllGoroutines has nothing
// to stop.
func TestCache_StopAllGoroutinesWithoutExpiration(t *testing.T) {
	t.Parallel()

	c := Must(&Options[int, int]{MaximumSize: 10})
	require.False(t, c.StopAllGoroutines())
}

// A clock whose Tick channel is closed delivers no ticks. The periodic clean-up goroutine returns
// instead of spinning on the always-ready channel, which would also keep the cache reachable.
func TestCache_ClosedTickChannelEndsPeriodicCleanUp(t *testing.T) {
	t.Parallel()

	clock := &manualTickClock{nonTickingClock: newNonTickingClock(), ticks: make(chan time.Time)}
	close(clock.ticks)
	c := Must(&Options[int, int]{
		MaximumSize:      10,
		ExpiryCalculator: ExpiryWriting[int, int](time.Hour),
		Clock:            clock,
	})
	t.Cleanup(func() { c.StopAllGoroutines() })
	c.Set(1, 1)

	select {
	case <-c.cache.done:
	case <-time.After(time.Second):
		require.Fail(t, "the periodic clean-up goroutine did not return on a closed tick channel")
	}
	v, ok := c.GetIfPresent(1)
	require.True(t, ok)
	require.Equal(t, 1, v)
}

// manualTickClock delivers a tick when the test sends one.
type manualTickClock struct {
	*nonTickingClock
	ticks chan time.Time
}

func (c *manualTickClock) Tick(time.Duration) <-chan time.Time {
	return c.ticks
}

// A tick that comes while an iteration or a save holds the policy's lock is skipped, so that
// StopAllGoroutines called from the loop body or the writer does not wait for a clean-up that waits
// for the lock its caller holds.
func TestCache_StopAllGoroutinesWhileIterating(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		iterate func(c *Cache[int, int], body func())
	}{
		{name: "Hottest", iterate: func(c *Cache[int, int], body func()) {
			for range c.Hottest() {
				body()
				break
			}
		}},
		{name: "Coldest", iterate: func(c *Cache[int, int], body func()) {
			for range c.Coldest() {
				body()
				break
			}
		}},
		{name: "SaveCacheTo", iterate: func(c *Cache[int, int], body func()) {
			require.NoError(t, SaveCacheTo(c, &callingWriter{call: body}))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clock := &manualTickClock{nonTickingClock: newNonTickingClock(), ticks: make(chan time.Time)}
			c := Must(&Options[int, int]{
				MaximumSize:      100,
				ExpiryCalculator: ExpiryWriting[int, int](time.Hour),
				Clock:            clock,
			})
			for i := range 10 {
				c.Set(i, i)
			}

			ran := false
			completes(t, "StopAllGoroutines in "+tt.name, func() {
				tt.iterate(c, func() {
					ran = true
					// the channel is unbuffered: the periodic goroutine has the tick
					clock.ticks <- time.Time{}
					require.True(t, c.StopAllGoroutines())
				})
			})
			require.True(t, ran)
		})
	}
}

// callingWriter calls call on the second write: the first one, of the maximum, comes before
// SaveCacheTo takes the policy's lock.
type callingWriter struct {
	call func()
	n    int
}

func (w *callingWriter) Write(p []byte) (int, error) {
	w.n++
	if w.n == 2 {
		w.call()
	}
	return len(p), nil
}

// A tick that comes while the policy's lock is held is skipped, but it leaves a drain required,
// which the holder runs when it releases the lock. Otherwise an idle cache would keep expired
// entries until a tick or a write that may never come.
func TestCache_SkippedTickCleansUpOnceTheLockIsReleased(t *testing.T) {
	t.Parallel()

	clock := &manualTickClock{nonTickingClock: newNonTickingClock(), ticks: make(chan time.Time)}
	var expired atomic.Int64
	c := Must(&Options[int, int]{
		MaximumSize:      100,
		ExpiryCalculator: ExpiryWriting[int, int](time.Second),
		Clock:            clock,
		OnDeletion: func(e DeletionEvent[int, int]) {
			if e.Cause == CauseExpiration {
				expired.Add(1)
			}
		},
	})
	t.Cleanup(func() { c.StopAllGoroutines() })
	for i := range 10 {
		c.Set(i, i)
	}
	c.CleanUp()

	for range c.Hottest() {
		clock.Sleep(2 * time.Second)
		clock.ticks <- time.Time{}
		require.Eventually(t, func() bool {
			return c.cache.drainStatus.Load() == required
		}, 5*time.Second, time.Millisecond, "the skipped tick left no drain required")
		break
	}
	require.Eventually(t, func() bool {
		return expired.Load() == 10
	}, 5*time.Second, time.Millisecond)
}
