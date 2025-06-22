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
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/stats"
)

const (
	unreachable = math.MaxUint32
)

func cleanup(c *Cache[int, int]) {
	c.CleanUp()
	time.Sleep(time.Second)
}

func firstBeforeAccess(c *Cache[int, int]) node.Node[int, int] {
	c.cache.evictionMutex.Lock()
	defer c.cache.evictionMutex.Unlock()
	return c.cache.evictionPolicy.probation.Head()
}

func updateRecency(t *testing.T, c *Cache[int, int], isRead bool, fn func()) {
	first := firstBeforeAccess(c)

	fn()
	c.cache.evictionMutex.Lock()
	defer c.cache.evictionMutex.Unlock()
	c.cache.maintenance(nil)

	if isRead {
		require.NotEqual(t, first, c.cache.evictionPolicy.probation.Head())
		require.Equal(t, first, c.cache.evictionPolicy.protected.Tail())
	} else {
		require.NotEqual(t, first.Key(), c.cache.evictionPolicy.probation.Head().Key())
		require.Equal(t, first.Key(), c.cache.evictionPolicy.protected.Tail().Key())
	}
}

func prepareAdaptation(t *testing.T, c *Cache[int, int], recencyBias bool) {
	c.cache.evictionMutex.Lock()
	k := -1
	if recencyBias {
		k = 1
	}
	c.cache.evictionPolicy.stepSize = float64(k) * math.Abs(c.cache.evictionPolicy.stepSize)
	maximum := c.cache.evictionPolicy.maximum
	c.cache.evictionPolicy.windowMaximum = uint64(0.5 * float64(maximum))
	c.cache.evictionPolicy.mainProtectedMaximum = uint64(percentMainProtected * float64(maximum-c.cache.evictionPolicy.windowMaximum))
	c.cache.evictionMutex.Unlock()

	c.InvalidateAll()
	for i := 0; i < int(maximum); i++ {
		v, ok := c.Set(i, i)
		require.True(t, ok)
		require.Equal(t, i, v)
	}

	for k := range c.All() {
		require.True(t, c.has(k))
		c.CleanUp()
	}
	for k := range c.All() {
		require.True(t, c.has(k))
		c.CleanUp()
	}
}

func adapt(t *testing.T, c *Cache[int, int], sampleSize uint64) {
	c.cache.evictionMutex.Lock()
	c.cache.evictionPolicy.previousSampleHitRate = 0.8
	c.cache.evictionPolicy.missesInSample = sampleSize / 2
	c.cache.evictionPolicy.hitsInSample = sampleSize - c.cache.evictionPolicy.missesInSample
	c.cache.climb()
	c.cache.evictionMutex.Unlock()

	for k := range c.All() {
		require.True(t, c.has(k))
		c.CleanUp()
	}
}

func TestCache_Eviction(t *testing.T) {
	t.Parallel()

	t.Run("overflow_add_one", func(t *testing.T) {
		t.Parallel()

		m := make(map[DeletionCause]int)
		c := Must(&Options[int, int]{
			MaximumWeight: unreachable - 1,
			Weigher: func(key int, value int) uint32 {
				return unreachable
			},
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				m[e.Cause]++
			},
		})

		v, ok := c.Set(1, 1)
		require.True(t, ok)
		require.Equal(t, 1, v)
		cleanup(c)
		require.Equal(t, uint64(0), c.WeightedSize())
		require.Equal(t, 1, m[CauseOverflow])
		require.Equal(t, 1, len(m))
	})
	t.Run("overflow_add_many", func(t *testing.T) {
		t.Parallel()

		m := make(map[DeletionCause]int)
		var mutex sync.Mutex
		c := Must(&Options[int, int]{
			MaximumWeight: unreachable - 1,
			Weigher: func(key int, value int) uint32 {
				return unreachable
			},
			OnDeletion: func(e DeletionEvent[int, int]) {
				mutex.Lock()
				m[e.Cause]++
				mutex.Unlock()
			},
		})

		for i := 0; i < 10; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)
		mutex.Lock()
		defer mutex.Unlock()
		require.Equal(t, uint64(0), c.WeightedSize())
		require.Equal(t, 10, m[CauseOverflow])
		require.Equal(t, 1, len(m))
	})
	t.Run("overflow_update_many", func(t *testing.T) {
		t.Parallel()

		m := make(map[DeletionCause][]int)
		count := make(map[int]int)
		var mutex sync.Mutex
		c := Must(&Options[int, int]{
			MaximumWeight: unreachable - 1,
			Weigher: func(key int, value int) uint32 {
				count[key]++
				if count[key] == 1 {
					return 1
				}
				return unreachable
			},
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				mutex.Lock()
				m[e.Cause] = append(m[e.Cause], e.Key)
				mutex.Unlock()
			},
		})

		for i := 0; i < 10; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		for i := 0; i < 10; i++ {
			v, ok := c.Set(i, i+1)
			require.False(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)
		require.Equal(t, uint64(0), c.WeightedSize())
		require.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, m[CauseOverflow])
		require.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, m[CauseReplacement])
		require.Equal(t, 2, len(m))
	})
	t.Run("evict_alreadyRemoved", func(t *testing.T) {
		t.Parallel()

		m := make(map[DeletionCause]int)
		c := Must(&Options[int, int]{
			MaximumSize: 1,
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				m[e.Cause]++
			},
		})

		k1 := 1
		k2 := k1 * 10
		v, ok := c.Set(k1, k1)
		require.True(t, ok)
		require.Equal(t, k1, v)

		c.cache.evictionMutex.Lock()
		n := c.cache.hashmap.Get(k1)
		require.True(t, n.IsAlive())
		done := make(chan struct{})
		go func() {
			v, ok := c.Set(k2, k2+1)
			require.True(t, ok)
			require.Equal(t, k2+1, v)
			v, inv := c.Invalidate(k1)
			require.True(t, inv)
			require.Equal(t, k1, v)
			done <- struct{}{}
		}()

		<-done
		require.False(t, c.has(k1))
		require.True(t, !n.IsAlive())
		require.True(t, n.IsRetired())
		c.cache.evictionMutex.Unlock()
		cleanup(c)
		require.True(t, n.IsDead())
		require.True(t, c.has(k2))
		require.Equal(t, 1, m[CauseInvalidation])
		require.Equal(t, 1, len(m))
	})
	t.Run("evict_candidate_lru", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		m := make(map[DeletionCause]int)
		s := stats.NewCounter()
		c := Must(&Options[int, int]{
			MaximumSize:   maximum,
			StatsRecorder: s,
			OnDeletion: func(e DeletionEvent[int, int]) {
				m[e.Cause]++
			},
		})
		c.cache.evictionPolicy.mainProtectedMaximum = 0
		c.cache.evictionPolicy.windowMaximum = maximum
		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)
		expected := make([]int, 0, maximum)
		c.cache.evictionMutex.Lock()
		h := c.cache.evictionPolicy.window.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		c.cache.evictionPolicy.windowMaximum = 0
		candidate := c.cache.evictionPolicy.evictFromWindow()
		require.False(t, node.Equals(candidate, nil))

		actual := make([]int, 0, maximum)
		h = c.cache.evictionPolicy.probation.Head()
		for !node.Equals(h, nil) {
			actual = append(actual, h.Key())
			h = h.Next()
		}
		c.cache.evictionMutex.Unlock()

		require.Equal(t, expected, actual)
	})
	t.Run("evict_victim_lru", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		var mutex sync.Mutex
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		c := Must(&Options[int, int]{
			MaximumSize:   maximum,
			StatsRecorder: s,
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				mutex.Lock()
				m[e.Cause]++
				actual = append(actual, e.Key)
				mutex.Unlock()
			},
		})
		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		c.cache.evictionMutex.Lock()
		c.cache.evictionPolicy.windowMaximum = 0
		candidate := c.cache.evictionPolicy.evictFromWindow()
		require.False(t, node.Equals(candidate, nil))

		expected := make([]int, 0, maximum)
		h := c.cache.evictionPolicy.probation.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		h = c.cache.evictionPolicy.protected.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		c.cache.evictionMutex.Unlock()
		c.SetMaximum(0)
		c.CleanUp()

		require.Equal(t, expected, actual)
	})
	t.Run("evict_window_candidates", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		var mutex sync.Mutex
		c := Must(&Options[int, int]{
			MaximumSize:   maximum,
			StatsRecorder: s,
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				mutex.Lock()
				m[e.Cause]++
				actual = append(actual, e.Key)
				mutex.Unlock()
			},
		})

		c.cache.evictionPolicy.windowMaximum = maximum / 2
		c.cache.evictionPolicy.mainProtectedMaximum = 0

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		for i := range c.cache.evictionPolicy.sketch.table {
			c.cache.evictionPolicy.sketch.table[i] = 0
		}

		expected := make([]int, 0, maximum)
		h := c.cache.evictionPolicy.window.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}

		c.cache.evictionPolicy.maximum = maximum / 2
		c.cache.evictionPolicy.windowMaximum = 0
		c.cache.evictNodes()

		require.Equal(t, expected, actual)
	})
	t.Run("evict_window_fallback", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		var mutex sync.Mutex
		c := Must(&Options[int, int]{
			MaximumSize:   maximum,
			StatsRecorder: s,
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				mutex.Lock()
				m[e.Cause]++
				actual = append(actual, e.Key)
				mutex.Unlock()
			},
		})

		c.cache.evictionPolicy.windowMaximum = maximum / 2
		c.cache.evictionPolicy.mainProtectedMaximum = 0

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		for i := range c.cache.evictionPolicy.sketch.table {
			c.cache.evictionPolicy.sketch.table[i] = 0
		}

		expected := make([]int, 0, maximum)
		h := c.cache.evictionPolicy.window.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}

		c.cache.evictionPolicy.maximum = maximum / 2
		c.cache.evictNodes()

		require.Equal(t, expected, actual)
	})
	t.Run("evict_candidateIsVictim", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		var mutex sync.Mutex
		c := Must(&Options[int, int]{
			MaximumSize:   maximum,
			StatsRecorder: s,
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				mutex.Lock()
				m[e.Cause]++
				actual = append(actual, e.Key)
				mutex.Unlock()
			},
		})
		e := c.cache.evictionPolicy

		e.windowMaximum = maximum / 2
		e.mainProtectedMaximum = maximum / 2

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		for !e.probation.IsEmpty() {
			n := e.probation.PopFront()
			e.protected.PushBack(n)
			n.MakeMainProtected()
		}
		for i := range c.cache.evictionPolicy.sketch.table {
			c.cache.evictionPolicy.sketch.table[i] = 0
		}
		e.mainProtectedWeightedSize = maximum - e.windowWeightedSize

		expected := make([]int, 0, maximum)
		h := e.window.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		h = e.probation.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		h = e.protected.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}

		e.windowMaximum = 0
		e.mainProtectedMaximum = 0
		e.maximum = 0
		c.cache.evictNodes()

		require.Equal(t, expected, actual)
	})
	t.Run("evict_toZero", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		var mutex sync.Mutex
		c := Must(&Options[int, int]{
			MaximumSize:   maximum,
			StatsRecorder: s,
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				mutex.Lock()
				m[e.Cause]++
				actual = append(actual, e.Key)
				mutex.Unlock()
			},
		})
		e := c.cache.evictionPolicy

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		for i := 0; i < len(c.cache.evictionPolicy.sketch.table); i++ {
			e.sketch.table[i] = 0
		}

		expected := make([]int, 0, maximum)
		h := e.window.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		h = e.probation.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		h = e.protected.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}

		c.SetMaximum(0)
		c.cache.evictNodes()

		require.Equal(t, expected, actual)
	})
	t.Run("evict_retired_candidate", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		c := Must(&Options[int, int]{
			MaximumSize:   maximum,
			StatsRecorder: s,
			OnDeletion: func(e DeletionEvent[int, int]) {
				m[e.Cause]++
				actual = append(actual, e.Key)
			},
		})
		e := c.cache.evictionPolicy

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		c.cache.evictionMutex.Lock()
		expected := e.window.Head()
		key := expected.Key()

		done := make(chan struct{})
		go func() {
			v, inv := c.Invalidate(key)
			require.True(t, inv)
			require.NotZero(t, v)
			done <- struct{}{}
		}()

		<-done
		require.True(t, !c.has(key))
		require.True(t, expected.IsRetired())

		e.windowMaximum--
		e.maximum--
		c.cache.evictNodes()

		require.True(t, expected.IsDead())
		require.Equal(t, e.maximum, uint64(c.EstimatedSize()))
		c.cache.evictionMutex.Unlock()
	})
	t.Run("evict_retired_victim", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		c := Must(&Options[int, int]{
			MaximumSize:   maximum,
			StatsRecorder: s,
			OnDeletion: func(e DeletionEvent[int, int]) {
				m[e.Cause]++
				actual = append(actual, e.Key)
			},
		})
		e := c.cache.evictionPolicy

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i+1)
			require.True(t, ok)
			require.Equal(t, i+1, v)
		}
		cleanup(c)

		c.cache.evictionMutex.Lock()
		expected := e.probation.Head()
		key := expected.Key()

		done := make(chan struct{})
		go func() {
			v, inv := c.Invalidate(key)
			require.True(t, inv)
			require.NotZero(t, v)
			done <- struct{}{}
		}()

		<-done
		require.True(t, !c.has(key))
		require.True(t, expected.IsRetired())

		e.windowMaximum--
		e.maximum--
		c.cache.evictNodes()

		require.True(t, expected.IsDead())
		require.Equal(t, e.maximum, uint64(c.EstimatedSize()))
		c.cache.evictionMutex.Unlock()
	})
	t.Run("evict_zeroWeight_candidate", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		var mutex sync.Mutex
		c := Must(&Options[int, int]{
			MaximumWeight: maximum,
			Weigher: func(key int, value int) uint32 {
				return uint32(value)
			},
			StatsRecorder: s,
			OnDeletion: func(e DeletionEvent[int, int]) {
				mutex.Lock()
				m[e.Cause]++
				actual = append(actual, e.Key)
				mutex.Unlock()
			},
		})
		e := c.cache.evictionPolicy

		for i := 0; i < maximum; i++ {
			v := 1
			if i == 0 {
				v = 0
			}
			_, ok := c.Set(i, v)
			require.True(t, ok)
		}
		cleanup(c)

		candidate := e.window.Head()
		c.SetMaximum(0)
		c.cache.evictNodes()

		require.True(t, c.has(candidate.Key()))
		require.Equal(t, uint64(0), c.WeightedSize())
		require.Equal(t, uint64(0), c.GetMaximum())
	})
	t.Run("evict_admit", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		var mutex sync.Mutex
		c := Must(&Options[int, int]{
			MaximumWeight: maximum,
			Weigher: func(key int, value int) uint32 {
				return uint32(value)
			},
			StatsRecorder: s,
			OnDeletion: func(e DeletionEvent[int, int]) {
				mutex.Lock()
				m[e.Cause]++
				actual = append(actual, e.Key)
				mutex.Unlock()
			},
		})
		e := c.cache.evictionPolicy

		e.sketch.ensureCapacity(maximum)
		candidate := 0
		victim := 1

		require.False(t, e.admit(candidate, victim))
		e.sketch.increment(candidate)
		require.True(t, e.admit(candidate, victim))

		for i := 0; i < 15; i++ {
			e.sketch.increment(victim)
			require.False(t, e.admit(candidate, victim))
		}

		for e.sketch.frequency(candidate) < admitHashdosThreshold {
			e.sketch.increment(candidate)
		}

		allow := 0
		rejected := 0
		count := uint32(3859390116)
		e.rand = func() uint32 {
			c := count
			count++
			return c
		}
		for i := 0; i < 1000; i++ {
			if e.admit(candidate, victim) {
				allow++
			} else {
				rejected++
			}
		}
		require.Equal(t, 992, rejected)
		require.Equal(t, 8, allow)
	})
	t.Run("updateRecency_onGet", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		c := Must(&Options[int, int]{
			MaximumSize:   maximum,
			StatsRecorder: s,
			OnDeletion: func(e DeletionEvent[int, int]) {
				m[e.Cause]++
				actual = append(actual, e.Key)
			},
		})

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		first := firstBeforeAccess(c)
		updateRecency(t, c, true, func() {
			require.True(t, c.has(first.Key()))
		})
	})
	t.Run("updateRecency_onSetIfAbsent", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		c := Must(&Options[int, int]{
			MaximumSize:   maximum,
			StatsRecorder: s,
			OnDeletion: func(e DeletionEvent[int, int]) {
				m[e.Cause]++
				actual = append(actual, e.Key)
			},
		})

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		first := firstBeforeAccess(c)
		updateRecency(t, c, false, func() {
			v, ok := c.SetIfAbsent(first.Key(), first.Value()+1)
			require.False(t, ok)
			require.Equal(t, first.Value(), v)
		})
	})
	t.Run("updateRecency_onSet", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		var mutex sync.Mutex
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		c := Must(&Options[int, int]{
			MaximumSize:   maximum,
			StatsRecorder: s,
			OnDeletion: func(e DeletionEvent[int, int]) {
				mutex.Lock()
				m[e.Cause]++
				actual = append(actual, e.Key)
				mutex.Unlock()
			},
		})

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		first := firstBeforeAccess(c)
		updateRecency(t, c, false, func() {
			v, ok := c.Set(first.Key(), first.Value()+1)
			require.False(t, ok)
			require.Equal(t, first.Value(), v)
		})
	})
	t.Run("adapt_increaseWindow", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		var mutex sync.Mutex
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		c := Must(&Options[int, int]{
			MaximumWeight: maximum * 10,
			Weigher: func(key int, value int) uint32 {
				return 10
			},
			StatsRecorder: s,
			OnDeletion: func(e DeletionEvent[int, int]) {
				mutex.Lock()
				m[e.Cause]++
				actual = append(actual, e.Key)
				mutex.Unlock()
			},
		})

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		e := c.cache.evictionPolicy

		prepareAdaptation(t, c, false)
		c.cache.evictionMutex.Lock()
		sampleSize := e.sketch.sampleSize
		protectedSize := e.mainProtectedWeightedSize
		protectedMaximum := e.mainProtectedMaximum
		windowSize := e.windowWeightedSize
		windowMaximum := e.windowMaximum
		c.cache.evictionMutex.Unlock()

		adapt(t, c, sampleSize)

		c.cache.evictionMutex.Lock()
		require.Less(t, e.mainProtectedWeightedSize, protectedSize)
		require.Less(t, e.mainProtectedMaximum, protectedMaximum)
		require.Greater(t, e.windowWeightedSize, windowSize)
		require.Greater(t, e.windowMaximum, windowMaximum)
		c.cache.evictionMutex.Unlock()
	})
	t.Run("adapt_decreaseWindow", func(t *testing.T) {
		t.Parallel()

		const maximum = 50
		m := make(map[DeletionCause]int)
		actual := make([]int, 0, maximum)
		s := stats.NewCounter()
		var mutex sync.Mutex
		c := Must(&Options[int, int]{
			MaximumSize:   maximum,
			StatsRecorder: s,
			OnDeletion: func(e DeletionEvent[int, int]) {
				mutex.Lock()
				m[e.Cause]++
				actual = append(actual, e.Key)
				mutex.Unlock()
			},
		})

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		e := c.cache.evictionPolicy

		prepareAdaptation(t, c, true)
		c.cache.evictionMutex.Lock()
		sampleSize := e.sketch.sampleSize
		protectedSize := e.mainProtectedWeightedSize
		protectedMaximum := e.mainProtectedMaximum
		windowSize := e.windowWeightedSize
		windowMaximum := e.windowMaximum
		c.cache.evictionMutex.Unlock()

		adapt(t, c, sampleSize)

		c.cache.evictionMutex.Lock()
		require.Greater(t, e.mainProtectedWeightedSize, protectedSize)
		require.Greater(t, e.mainProtectedMaximum, protectedMaximum)
		require.Less(t, e.windowWeightedSize, windowSize)
		require.Less(t, e.windowMaximum, windowMaximum)
		c.cache.evictionMutex.Unlock()
	})
}

func TestCache_CornerCases(t *testing.T) {
	t.Parallel()

	t.Run("withoutRefresh", func(t *testing.T) {
		t.Parallel()

		c := &Cache[int, int]{
			cache: &cache[int, int]{},
		}
		ctx := context.Background()

		require.NotPanics(t, func() {
			require.Nil(t, c.Refresh(ctx, 1, nil))
			require.Nil(t, c.BulkRefresh(ctx, []int{1}, nil))
			require.Nil(t, c.cache.refreshKey(ctx, refreshableKey[int, int]{}, nil))
			c.SetRefreshableAfter(1, time.Hour)
			c.SetRefreshableAfter(1, -time.Hour)
		})
	})
	t.Run("BulkRefresh_withEmptyKeys", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{
			RefreshCalculator: RefreshWriting[int, int](time.Hour),
		})
		ctx := context.Background()

		ch := c.BulkRefresh(ctx, []int{}, nil)
		results := <-ch
		require.Empty(t, results)
	})
	t.Run("withoutExpiration", func(t *testing.T) {
		t.Parallel()

		c := &Cache[int, int]{
			cache: &cache[int, int]{},
		}

		require.NotPanics(t, func() {
			c.cache.setExpiresAfterRead(nil, 0, -time.Hour)
		})
	})
	t.Run("withoutMaintenance", func(t *testing.T) {
		t.Parallel()

		c := &Cache[int, int]{
			cache: &cache[int, int]{},
		}

		require.NotPanics(t, func() {
			c.cache.makeDead(nil)
			require.Equal(t, uint64(0), c.WeightedSize())
			c.SetMaximum(0)
		})
	})
	t.Run("invalidTask", func(t *testing.T) {
		t.Parallel()

		c := &Cache[int, int]{
			cache: &cache[int, int]{},
		}

		require.Panics(t, func() {
			c.cache.runTask(&task[int, int]{
				writeReason: unknownReason,
			})
		})
	})
	t.Run("withoutDelete", func(t *testing.T) {
		t.Parallel()

		c := &Cache[int, int]{
			cache: &cache[int, int]{},
		}

		require.NotPanics(t, func() {
			c.cache.afterDelete(nil, 0, false)
		})
	})
}

func TestCache_Scheduler(t *testing.T) {
	t.Parallel()

	t.Run("scheduleAfterWrite", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{})

		c.cache.evictionMutex.Lock()
		defer c.cache.evictionMutex.Unlock()

		transitions := map[uint32]uint32{
			idle:                 required,
			required:             required,
			processingToIdle:     processingToRequired,
			processingToRequired: processingToRequired,
		}

		for from, to := range transitions {
			c.cache.drainStatus.Store(from)
			c.cache.scheduleAfterWrite()
			require.Equal(t, to, c.cache.drainStatus.Load())
		}

		require.Panics(t, func() {
			c.cache.drainStatus.Store(10 * processingToRequired)
			c.cache.scheduleAfterWrite()
		})
	})
	t.Run("scheduleDrainBuffers", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{
			MaximumSize: 1,
		})
		c.cache.executor = func(fn func()) {
		}

		transitions := map[uint32]uint32{
			idle:                 processingToIdle,
			required:             processingToIdle,
			processingToIdle:     processingToIdle,
			processingToRequired: processingToRequired,
		}

		for from, to := range transitions {
			c.cache.drainStatus.Store(from)
			c.cache.scheduleDrainBuffers()
			require.Equal(t, to, c.cache.drainStatus.Load())
		}
	})
	t.Run("rescheduleDrainBuffers", func(t *testing.T) {
		t.Parallel()

		done := make(chan struct{})
		evicting := make(chan struct{})
		onDeletion := func(e DeletionEvent[int, int]) {
			evicting <- struct{}{}
			<-done
		}
		c := Must(&Options[int, int]{
			MaximumSize:      1,
			OnAtomicDeletion: onDeletion,
		})

		v1, ok := c.Set(1, 1)
		require.True(t, ok)
		require.Equal(t, 1, v1)
		v1, ok = c.Set(2, 2)
		require.True(t, ok)
		require.Equal(t, 2, v1)
		<-evicting

		v2, ok := c.Set(3, 3)
		require.True(t, ok)
		require.Equal(t, 3, v2)
		require.Equal(t, processingToRequired, c.cache.drainStatus.Load())

		done <- struct{}{}
	})
	t.Run("shouldDrainBuffers_invalidDrainStatus", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{})

		require.Panics(t, func() {
			c.cache.drainStatus.Store(10 * processingToRequired)
			c.cache.shouldDrainBuffers(true)
		})
	})
	t.Run("weightedSize_maintenance", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{
			MaximumWeight: 100,
			Weigher: func(key int, value int) uint32 {
				return uint32(key)
			},
		})

		for i := 0; i < 10; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		c.cache.drainStatus.Store(required)
		require.Equal(t, uint64(45), c.WeightedSize())
		require.Equal(t, idle, c.cache.drainStatus.Load())
	})
	t.Run("getMaximum_maintenance", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{
			MaximumSize: 10,
		})

		for i := 0; i < 10; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		c.cache.drainStatus.Store(required)
		require.Equal(t, uint64(10), c.GetMaximum())
		require.Equal(t, idle, c.cache.drainStatus.Load())
	})
}
