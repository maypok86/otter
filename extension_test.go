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
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/stats"
)

func TestCache_SetExpiresAfter(t *testing.T) {
	size := 100
	statsCounter := stats.NewCounter()
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	done := make(chan struct{})
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: ExpiryWriting[int, int](time.Second),
		OnDeletion: func(e DeletionEvent[int, int]) {
			defer func() {
				done <- struct{}{}
			}()

			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		},
	})

	k1 := 1
	v1 := 100

	c.SetExpiresAfter(k1, -2*time.Second)
	_, ok := c.GetEntryQuietly(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	c.SetExpiresAfter(k1, 2*time.Second)
	_, ok = c.GetEntry(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	c.Set(k1, v1)
	e, ok := c.GetEntry(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if e.Value != v1 {
		t.Fatalf("value should be equal to v1. key: %v, value: %v", k1, e.Value)
	}
	if expiresAfter := e.ExpiresAfter(); expiresAfter < 800*time.Millisecond || expiresAfter > time.Second {
		t.Fatalf("expiresAfter should be equal to %v. expiresAfter: %v", 200*time.Millisecond, expiresAfter)
	}
	c.SetExpiresAfter(k1, 2*time.Second)
	e, ok = c.GetEntryQuietly(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if e.Value != v1 {
		t.Fatalf("value should be equal to v1. key: %v, value: %v", k1, e.Value)
	}
	if expiresAfter := e.ExpiresAfter(); expiresAfter > 2*time.Second || expiresAfter < time.Second+800*time.Millisecond {
		t.Fatalf("expiresAfter should be equal to %v. expiresAfter: %v", time.Second, expiresAfter)
	}

	c.CleanUp()
	<-done
	mutex.Lock()
	if len(m) != 1 || m[CauseExpiration] != 1 {
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", 1, m[CauseExpiration])
	}
	mutex.Unlock()
	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 1 ||
		snapshot.Misses != 1 ||
		snapshot.Evictions != 1 ||
		snapshot.EvictionWeight != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_SetRefreshableAfter(t *testing.T) {
	t.Parallel()

	size := 100
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:       size,
		StatsRecorder:     statsCounter,
		RefreshCalculator: RefreshCreating[int, int](200 * time.Millisecond),
	})

	k1 := 1
	v1 := 100

	c.SetRefreshableAfter(k1, -2*time.Second)
	_, ok := c.GetEntryQuietly(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	c.SetRefreshableAfter(k1, 2*time.Second)
	_, ok = c.GetEntry(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	c.Set(k1, v1)
	e, ok := c.GetEntry(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if e.Value != v1 {
		t.Fatalf("value should be equal to v1. key: %v, value: %v", k1, e.Value)
	}
	if refreshableAfter := e.RefreshableAfter(); refreshableAfter > 200*time.Millisecond {
		t.Fatalf("refreshableAfter should be equal to %v. refreshableAfter: %v", 200*time.Millisecond, refreshableAfter)
	}
	c.SetRefreshableAfter(k1, time.Second)
	e, ok = c.GetEntryQuietly(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if e.Value != v1 {
		t.Fatalf("value should be equal to v1. key: %v, value: %v", k1, e.Value)
	}
	if refreshableAfter := e.RefreshableAfter(); refreshableAfter > time.Second || refreshableAfter < 500*time.Millisecond {
		t.Fatalf("refreshableAfter should be equal to %v. refreshableAfter: %v", time.Second, refreshableAfter)
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 1 ||
		snapshot.Misses != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_Extension(t *testing.T) {
	size := getRandomSize(t)

	duration := time.Hour
	c := Must(&Options[int, int]{
		MaximumSize:       size,
		ExpiryCalculator:  ExpiryWriting[int, int](duration),
		RefreshCalculator: RefreshWriting[int, int](duration),
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	k1 := 4
	v1 := k1
	e1, ok := c.GetEntryQuietly(k1)
	if !ok {
		t.Fatalf("not found key %d", k1)
	}

	e2, ok := c.GetEntry(k1)
	if !ok {
		t.Fatalf("not found key %d", k1)
	}

	time.Sleep(time.Second)

	isEqualEntries := func(a, b Entry[int, int]) bool {
		return a.Key == b.Key &&
			a.Value == b.Value &&
			a.Weight == b.Weight &&
			a.ExpiresAtNano == b.ExpiresAtNano &&
			a.RefreshableAtNano == b.RefreshableAtNano
	}

	isValidEntries := e1.Key == k1 &&
		e1.Value == v1 &&
		e1.Weight == 1 &&
		isEqualEntries(e1, e2) &&
		e1.ExpiresAfter() < duration &&
		!e1.HasExpired()

	if !isValidEntries {
		t.Fatalf("found not valid entries. e1: %+v, e2: %+v, v1:%d", e1, e2, v1)
	}

	if _, ok := c.GetEntryQuietly(size); ok {
		t.Fatalf("found not valid key: %d", size)
	}
	if _, ok := c.GetEntry(size); ok {
		t.Fatalf("found not valid key: %d", size)
	}
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
	merge := func(first, second []node.Node[int, int], firstWins func(a, b node.Node[int, int]) bool) []int {
		keys := make([]int, 0, len(first)+len(second))
		for len(first) > 0 || len(second) > 0 {
			if len(second) == 0 || (len(first) > 0 && firstWins(first[0], second[0])) {
				keys = append(keys, first[0].Key())
				first = first[1:]
			} else {
				keys = append(keys, second[0].Key())
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
		freq := func(n node.Node[int, int]) uint64 {
			return p.sketch.frequency(n.Key())
		}
		keysOf := func(s []node.Node[int, int]) []int {
			keys := make([]int, 0, len(s))
			for _, n := range s {
				keys = append(keys, n.Key())
			}
			return keys
		}
		window := slices.Collect(p.window.All())
		probation := slices.Collect(p.probation.All())
		protected := slices.Collect(p.protected.All())

		wantColdest = merge(window, probation, func(w, pr node.Node[int, int]) bool {
			return freq(w) <= freq(pr)
		})
		wantColdest = append(wantColdest, keysOf(protected)...)

		slices.Reverse(window)
		slices.Reverse(probation)
		slices.Reverse(protected)
		wantHottest = keysOf(protected)
		wantHottest = append(wantHottest, merge(probation, window, func(pr, w node.Node[int, int]) bool {
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

// newCacheWithExpiredEntry returns a cache whose key 1 has expired but is still in the table:
// the executor never runs maintenance, which would remove it.
func newCacheWithExpiredEntry(t *testing.T, bounded bool) *Cache[int, int] {
	t.Helper()

	clk := newNonTickingClock()
	opts := &Options[int, int]{
		Clock:            clk,
		ExpiryCalculator: ExpiryWriting[int, int](time.Minute),
		Executor:         func(fn func()) {},
	}
	if bounded {
		opts.MaximumSize = 10
	}
	c := Must(opts)
	c.Set(1, 1)
	clk.Sleep(2 * time.Minute)
	require.NotNil(t, c.cache.hashmap.Get(1), "the expired entry must still be in the table")
	return c
}

// Set over an expired entry reports that the key had no value, as SetIfAbsent, Compute and
// reads do.
func TestCache_SetOverExpiredEntryReportsAbsent(t *testing.T) {
	t.Parallel()

	for _, bounded := range []bool{false, true} {
		t.Run(fmt.Sprintf("bounded=%v", bounded), func(t *testing.T) {
			t.Parallel()

			c := newCacheWithExpiredEntry(t, bounded)
			v, ok := c.Set(1, 2)
			require.True(t, ok)
			require.Equal(t, 2, v)
		})
	}
}

// Invalidate of an expired entry reports that the key was not present. The listeners still get
// the expired value with CauseExpiration.
func TestCache_InvalidateOfExpiredEntryReportsAbsent(t *testing.T) {
	t.Parallel()

	for _, bounded := range []bool{false, true} {
		t.Run(fmt.Sprintf("bounded=%v", bounded), func(t *testing.T) {
			t.Parallel()

			c := newCacheWithExpiredEntry(t, bounded)
			v, ok := c.Invalidate(1)
			require.False(t, ok)
			require.Zero(t, v)
			require.Nil(t, c.cache.hashmap.Get(1), "the expired entry was not removed")
		})
	}
}
