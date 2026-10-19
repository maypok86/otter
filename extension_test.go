// Copyright (c) 2024 Alexey Mayshev and contributors. All rights reserved.
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
	"errors"
	"fmt"
	"iter"
	"math/rand/v2"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

// SetExpiresAfter sets the deadline of a present entry only, and the entry expires at it, not at
// the deadline it had before.
func TestCache_SetExpiresAfter(t *testing.T) {
	t.Parallel()

	forEachSpec(t, cacheSpec{expiry: withExpiry, executor: allExecutors}, func(t *testing.T, cc *cacheContext) {
		cc.cache.SetExpiresAfter(1, -2*time.Second)
		cc.cache.SetExpiresAfter(1, 2*time.Second)
		_, ok := cc.cache.GetEntry(1)
		require.False(t, ok, "a deadline brought an absent key in")

		cc.cache.Set(1, 100)
		e, ok := cc.cache.GetEntry(1)
		require.True(t, ok)
		require.Equal(t, 100, e.Value)
		require.Equal(t, specExpiry, e.ExpiresAfter())

		cc.cache.SetExpiresAfter(1, 2*specExpiry)
		e, ok = cc.cache.GetEntryQuietly(1)
		require.True(t, ok)
		require.Equal(t, 100, e.Value)
		require.Equal(t, 2*specExpiry, e.ExpiresAfter())

		cc.advance(specExpiry) // the former deadline
		cc.cache.CleanUp()
		cc.run()
		cc.requireValue(t, 1, 100)
		cc.requireDeletions(t)

		cc.advance(specExpiry)
		cc.cache.CleanUp()
		cc.run()
		cc.requireDeletions(t, DeletionEvent[int, int]{Key: 1, Value: 100, Cause: CauseExpiration})
		snapshot := cc.stats.Snapshot()
		require.Equal(t, uint64(1), snapshot.Hits)
		require.Equal(t, uint64(1), snapshot.Misses)
		require.Equal(t, uint64(1), snapshot.Evictions)
		require.Equal(t, uint64(1), snapshot.EvictionWeight)
	})
}

// SetRefreshableAfter sets the refresh time of a present entry only.
func TestCache_SetRefreshableAfter(t *testing.T) {
	t.Parallel()

	forEachSpec(t, cacheSpec{refresh: withRefresh, executor: allExecutors}, func(t *testing.T, cc *cacheContext) {
		cc.cache.SetRefreshableAfter(1, -2*time.Second)
		cc.cache.SetRefreshableAfter(1, 2*time.Second)
		_, ok := cc.cache.GetEntry(1)
		require.False(t, ok, "a refresh time brought an absent key in")

		cc.cache.Set(1, 100)
		e, ok := cc.cache.GetEntry(1)
		require.True(t, ok)
		require.Equal(t, 100, e.Value)
		require.Equal(t, specRefresh, e.RefreshableAfter())

		cc.cache.SetRefreshableAfter(1, 2*specRefresh)
		e, ok = cc.cache.GetEntryQuietly(1)
		require.True(t, ok)
		require.Equal(t, 100, e.Value)
		require.Equal(t, 2*specRefresh, e.RefreshableAfter())

		snapshot := cc.stats.Snapshot()
		require.Equal(t, uint64(1), snapshot.Hits)
		require.Equal(t, uint64(1), snapshot.Misses)
	})
}

// GetEntry and GetEntryQuietly return the same entry, and neither returns an absent key.
func TestCache_Extension(t *testing.T) {
	t.Parallel()

	spec := cacheSpec{expiry: withExpiry, refresh: withRefresh, population: []populationKind{partialCache, fullCache}}
	forEachSpec(t, spec, func(t *testing.T, cc *cacheContext) {
		for _, key := range cc.keys() {
			quiet, ok := cc.cache.GetEntryQuietly(key)
			require.True(t, ok)
			read, ok := cc.cache.GetEntry(key)
			require.True(t, ok)
			read.SnapshotAtNano = quiet.SnapshotAtNano
			require.Equal(t, quiet, read)
			require.Equal(t, Entry[int, int]{
				Key:               key,
				Value:             cc.original[key],
				Weight:            1,
				ExpiresAtNano:     quiet.SnapshotAtNano + int64(specExpiry),
				RefreshableAtNano: quiet.SnapshotAtNano + int64(specRefresh),
				SnapshotAtNano:    quiet.SnapshotAtNano,
			}, quiet)
		}
		_, ok := cc.cache.GetEntryQuietly(absentKey)
		require.False(t, ok)
		_, ok = cc.cache.GetEntry(absentKey)
		require.False(t, ok)
	})
}

func TestCache_Coldest(t *testing.T) {
	t.Parallel()

	t.Run("coldest_order", func(t *testing.T) {
		t.Parallel()

		const (
			maximum = 50
			weight  = 10
			entries = maximum / weight
		)

		c := Must(&Options[int, int]{
			MaximumWeight: maximum,
			Weigher: func(key int, value int) uint32 {
				return weight
			},
			InitialCapacity: 100,
			Executor: func(fn func()) {
				fn()
			},
		})
		keys := make([]int, 0, entries)
		for i := 0; i < entries; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
			keys = append(keys, i)
		}
		keys = keys[:len(keys)-1]

		coldest := make([]int, 0, entries)
		for e := range c.Coldest() {
			if e.Key == entries-1 {
				continue
			}
			coldest = append(coldest, e.Key)
		}

		require.Equal(t, keys, coldest)
	})
	t.Run("coldest_partial", func(t *testing.T) {
		t.Parallel()

		const (
			maximum = 50
			entries = maximum
		)

		c := Must(&Options[int, int]{
			MaximumSize:     maximum,
			InitialCapacity: 100,
			Executor: func(fn func()) {
				fn()
			},
		})
		keys := make([]int, 0, entries)
		for i := 0; i < entries; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
			keys = append(keys, i)
		}

		coldest := make([]int, 0, entries)
		i := 0
		for e := range c.Coldest() {
			if i >= maximum/2 {
				break
			}
			coldest = append(coldest, e.Key)
			i++
		}

		require.Subset(t, keys, coldest)
		require.ElementsMatch(t, slices.Collect(c.Keys()), keys)
	})
	t.Run("coldest_full", func(t *testing.T) {
		t.Parallel()

		const (
			maximum = 50
			entries = maximum
		)

		c := Must(&Options[int, int]{
			MaximumSize:     maximum,
			InitialCapacity: 100,
			Executor: func(fn func()) {
				fn()
			},
		})
		for i := 0; i < entries; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}

		coldest := make([]int, 0, maximum)
		for e := range c.Coldest() {
			coldest = append(coldest, e.Key)
		}

		require.ElementsMatch(t, slices.Collect(c.Keys()), coldest)
	})
}

func TestCache_Hottest(t *testing.T) {
	t.Parallel()

	t.Run("hottest_order", func(t *testing.T) {
		t.Parallel()

		const (
			maximum = 50
			entries = maximum
		)

		c := Must(&Options[int, int]{
			MaximumSize:     maximum,
			InitialCapacity: 100,
			Executor: func(fn func()) {
				fn()
			},
		})
		keys := make([]int, 0, entries)
		for i := 0; i < entries; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
			keys = append(keys, i)
		}
		keys = keys[:len(keys)-1]

		coldest := make([]int, 0, maximum)
		for _, e := range slices.Backward(slices.Collect(c.Hottest())) {
			if e.Key == maximum-1 {
				continue
			}
			coldest = append(coldest, e.Key)
		}

		require.Equal(t, keys, coldest)
	})
	t.Run("hottest_partial", func(t *testing.T) {
		t.Parallel()

		const (
			maximum = 50
			entries = maximum
		)

		c := Must(&Options[int, int]{
			MaximumSize:     maximum,
			InitialCapacity: 100,
			Executor: func(fn func()) {
				fn()
			},
		})
		keys := make([]int, 0, entries)
		for i := 0; i < entries; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
			keys = append(keys, i)
		}

		hottest := make([]int, 0, entries)
		i := 0
		for e := range c.Hottest() {
			if i >= maximum/2 {
				break
			}
			hottest = append(hottest, e.Key)
			i++
		}

		require.Subset(t, keys, hottest)
		require.ElementsMatch(t, slices.Collect(c.Keys()), keys)
	})
}

// Without a size bound, Hottest and Coldest iterate over the entries as All does, and stop when
// the loop body does.
func TestCache_EvictionOrderWithoutBound(t *testing.T) {
	t.Parallel()

	c := Must(&Options[int, int]{})
	for i := range 10 {
		c.Set(i, i)
	}
	for _, seq := range []iter.Seq[Entry[int, int]]{c.Hottest(), c.Coldest()} {
		n := 0
		for range seq {
			n++
			break
		}
		require.Equal(t, 1, n)
	}
}

// Hottest and Coldest hold the eviction lock, so maintenance cannot remove an entry that expires
// while they iterate. They skip it themselves: the loop body sees only live entries.
func TestCache_EvictionOrderSkipsEntriesExpiredDuringIt(t *testing.T) {
	t.Parallel()

	for _, hottest := range []bool{true, false} {
		clock := newNonTickingClock()
		c := Must(&Options[int, int]{
			MaximumSize:      10,
			ExpiryCalculator: ExpiryWriting[int, int](time.Minute),
			Clock:            clock,
		})
		for i := range 5 {
			c.Set(i, i)
		}
		seq := c.Coldest()
		if hottest {
			seq = c.Hottest()
		}
		seen := 0
		for range seq {
			seen++
			clock.Sleep(time.Minute)
		}
		require.Equal(t, 1, seen, "hottest=%v", hottest)
	}
}

// Hottest and Coldest merge the window and probation queues by frequency: Coldest takes the less
// frequent head first and Hottest the more frequent one. The merge used to go the other way.
func TestCache_EvictionOrderMergesByFrequency(t *testing.T) {
	t.Parallel()

	const maximum = 1000
	c := Must(&Options[int, int]{
		MaximumSize: maximum,
		Executor:    func(fn func()) { fn() },
	})
	z := rand.NewZipf(rand.New(rand.NewPCG(1, 7)), 1.1, 1, 50_000)
	for range 200_000 {
		k := int(z.Uint64())
		if _, ok := c.GetIfPresent(k); !ok {
			c.Set(k, k)
		}
	}
	c.CleanUp()

	// merge takes the head of first while firstWins says it goes before the head of second, and
	// the head of second otherwise.
	merge := func(first, second []node.Node, firstWins func(a, b node.Node) bool) []int {
		keys := make([]int, 0, len(first)+len(second))
		for len(first) > 0 || len(second) > 0 {
			if len(second) == 0 || (len(first) > 0 && firstWins(first[0], second[0])) {
				keys = append(keys, c.cache.nodeManager.Key(first[0]))
				first = first[1:]
			} else {
				keys = append(keys, c.cache.nodeManager.Key(second[0]))
				second = second[1:]
			}
		}
		return keys
	}
	var wantColdest, wantHottest []int
	func() {
		c.cache.evictionMutex.Lock()
		defer c.cache.evictionMutex.Unlock()
		p := c.cache.evictionPolicy
		freq := func(n node.Node) uint64 {
			return p.sketch.frequency(c.cache.nodeManager.Key(n))
		}
		keysOf := func(s []node.Node) []int {
			keys := make([]int, 0, len(s))
			for _, n := range s {
				keys = append(keys, c.cache.nodeManager.Key(n))
			}
			return keys
		}
		window := slices.Collect(p.window.All())
		probation := slices.Collect(p.probation.All())
		protected := slices.Collect(p.protected.All())

		wantColdest = merge(window, probation, func(w, pr node.Node) bool {
			return freq(w) <= freq(pr)
		})
		wantColdest = append(wantColdest, keysOf(protected)...)

		slices.Reverse(window)
		slices.Reverse(probation)
		slices.Reverse(protected)
		wantHottest = keysOf(protected)
		wantHottest = append(wantHottest, merge(probation, window, func(pr, w node.Node) bool {
			return freq(pr) >= freq(w)
		})...)
	}()

	collect := func(seq func(func(Entry[int, int]) bool)) []int {
		var keys []int
		for e := range seq {
			keys = append(keys, e.Key)
		}
		return keys
	}
	require.Equal(t, wantColdest, collect(c.Coldest()))
	require.Equal(t, wantHottest, collect(c.Hottest()))
}

// Set over an expired entry reports that the key had no value, as SetIfAbsent, Compute and
// reads do.
func TestCache_SetOverExpiredEntryReportsAbsent(t *testing.T) {
	t.Parallel()

	forEachSpec(t, prevExpired.spec(t, cacheSpec{}), func(t *testing.T, cc *cacheContext) {
		prevExpired.populate(cc)
		v, ok := cc.cache.Set(1, 2)
		require.True(t, ok)
		require.Equal(t, 2, v)
	})
}

// Invalidate of an expired entry reports that the key was not present. The listeners still get
// the expired value with CauseExpiration.
func TestCache_InvalidateOfExpiredEntryReportsAbsent(t *testing.T) {
	t.Parallel()

	forEachSpec(t, prevExpired.spec(t, cacheSpec{}), func(t *testing.T, cc *cacheContext) {
		prevExpired.populate(cc)
		v, ok := cc.cache.Invalidate(1)
		require.False(t, ok)
		require.Zero(t, v)
		require.Nil(t, cc.cache.nodeID(1), "the expired entry was not removed")
		cc.run()
		cc.requireDeletions(t, DeletionEvent[int, int]{Key: 1, Value: 1, Cause: CauseExpiration})
	})
}

// SetExpiresAfter does not bring back an expired entry that is still in the table: the entry is
// absent, so there is nothing to set a deadline for.
func TestCache_SetExpiresAfterDoesNotReviveExpiredEntry(t *testing.T) {
	t.Parallel()

	forEachSpec(t, prevExpired.spec(t, cacheSpec{}), func(t *testing.T, cc *cacheContext) {
		prevExpired.populate(cc)
		cc.cache.SetExpiresAfter(1, time.Hour)
		cc.requireValue(t, 1, 0)
	})
}

// A deadline that SetExpiresAfter moves earlier reaches the timer wheel through the write buffer,
// which does not drop tasks. Through the lossy read buffer alone, a dropped access left the entry
// in the wheel's bucket for its former deadline, so CleanUp did not remove it once it expired.
func TestCache_EarlierDeadlineReachesTheTimerWheel(t *testing.T) {
	t.Parallel()

	const n = 1000
	clk := newNonTickingClock()
	c := Must(&Options[int, int]{
		Clock:            clk,
		ExpiryCalculator: ExpiryWriting[int, int](time.Hour),
		// Maintenance runs only on CleanUp, so the read buffer fills up and drops accesses.
		Executor: func(fn func()) {},
	})
	for k := 0; k < n; k++ {
		c.Set(k, k)
	}
	c.CleanUp()

	for k := 0; k < n; k++ {
		c.SetExpiresAfter(k, time.Second)
	}
	clk.Sleep(2 * time.Second)
	c.CleanUp()

	survivors := 0
	for k := 0; k < n; k++ {
		if c.cache.hashmap.Get(k) != nil {
			survivors++
		}
	}
	require.Zero(t, survivors, "expired entries stayed in the table after CleanUp")
	require.Zero(t, c.EstimatedSize())
}

// setBesideHeldWrite calls set while a write of the same key is held between reading the key's
// deadline and refresh time and storing them back (the write is held at entered until proceed is
// closed). set is given time to complete before the write continues: without the bucket lock, it
// changed the node in that window, and the write then stored the times it had read over the change.
func setBesideHeldWrite(entered, proceed chan struct{}, writeDone <-chan struct{}, set func()) {
	<-entered
	setDone := make(chan struct{})
	go func() {
		defer close(setDone)
		set()
	}()
	select {
	case <-setDone:
	case <-time.After(100 * time.Millisecond):
	}
	close(proceed)
	<-writeDone
	<-setDone
}

// SetExpiresAfter and SetRefreshableAfter are not undone by a concurrent update. The calculators
// keep the deadline and refresh time on update, so in either order of the two calls the entry
// ends up with the times the setter asked for.
func TestCache_DeadlineSettersSurviveConcurrentUpdate(t *testing.T) {
	t.Parallel()

	for _, refresh := range []bool{false, true} {
		t.Run(fmt.Sprintf("refresh=%v", refresh), func(t *testing.T) {
			t.Parallel()

			t.Run("in place", func(t *testing.T) {
				t.Parallel()
				testDeadlineSetterSurvivesUpdate(t, 1, 2, refresh)
			})
			t.Run("replacement", func(t *testing.T) {
				t.Parallel()
				// the first update of an inline value replaces the node
				testDeadlineSetterSurvivesUpdate(t, "a", "b", refresh)
			})
		})
	}
}

func testDeadlineSetterSurvivesUpdate[V any](t *testing.T, v1, v2 V, refresh bool) {
	t.Helper()

	const d = time.Minute
	clk := newNonTickingClock()
	entered, proceed := make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	o := &Options[int, V]{
		Clock: clk,
		OnAtomicDeletion: func(e DeletionEvent[int, V]) {
			if armed.CompareAndSwap(true, false) {
				close(entered)
				<-proceed
			}
		},
	}
	if refresh {
		o.RefreshCalculator = RefreshCreating[int, V](time.Hour)
	} else {
		o.ExpiryCalculator = ExpiryCreating[int, V](time.Hour)
	}
	c := Must(o)
	c.Set(1, v1)

	armed.Store(true)
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		c.Set(1, v2)
	}()
	setBesideHeldWrite(entered, proceed, writeDone, func() {
		if refresh {
			c.SetRefreshableAfter(1, d)
		} else {
			c.SetExpiresAfter(1, d)
		}
	})

	e, ok := c.GetEntryQuietly(1)
	require.True(t, ok)
	require.Equal(t, v2, e.Value)
	got := e.ExpiresAtNano
	if refresh {
		got = e.RefreshableAtNano
	}
	require.Equal(t, clk.NowNano()+int64(d), got, "the update stored the time it read before the setter changed it")
}

// heldReloadFailure holds RefreshAfterReloadFailure, which runs under the bucket lock between
// reading the refresh time and storing the one it returns.
type heldReloadFailure[K comparable, V any] struct {
	RefreshCalculator[K, V]
	entered, proceed chan struct{}
}

func (h *heldReloadFailure[K, V]) RefreshAfterReloadFailure(entry Entry[K, V], err error) time.Duration {
	close(h.entered)
	<-h.proceed
	return h.RefreshCalculator.RefreshAfterReloadFailure(entry, err)
}

// SetRefreshableAfter is not undone by a concurrent failed reload, which keeps the refresh time.
func TestCache_SetRefreshableAfterSurvivesFailedReload(t *testing.T) {
	t.Parallel()

	const d = time.Minute
	clk := newNonTickingClock()
	calc := &heldReloadFailure[int, int]{
		RefreshCalculator: RefreshWriting[int, int](time.Hour),
		entered:           make(chan struct{}),
		proceed:           make(chan struct{}),
	}
	c := Must(&Options[int, int]{
		Clock:             clk,
		RefreshCalculator: calc,
	})
	c.Set(1, 1)

	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		<-c.Refresh(context.Background(), 1, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
			return 0, errors.New("reload failed")
		}))
	}()
	setBesideHeldWrite(calc.entered, calc.proceed, writeDone, func() {
		c.SetRefreshableAfter(1, d)
	})

	e, ok := c.GetEntryQuietly(1)
	require.True(t, ok)
	require.Equal(t, clk.NowNano()+int64(d), e.RefreshableAtNano, "the failed reload stored the refresh time it read before SetRefreshableAfter changed it")
}
