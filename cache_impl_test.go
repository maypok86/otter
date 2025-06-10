package otter

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/eviction/tinylfu"
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
	return c.cache.evictionPolicy.Probation.Head()
}

func updateRecency(t *testing.T, c *Cache[int, int], isRead bool, fn func()) {
	first := firstBeforeAccess(c)

	fn()
	c.cache.evictionMutex.Lock()
	defer c.cache.evictionMutex.Unlock()
	c.cache.maintenance(nil)

	if isRead {
		require.NotEqual(t, first, c.cache.evictionPolicy.Probation.Head())
		require.Equal(t, first, c.cache.evictionPolicy.Protected.Tail())
	} else {
		require.NotEqual(t, first.Key(), c.cache.evictionPolicy.Probation.Head().Key())
		require.Equal(t, first.Key(), c.cache.evictionPolicy.Protected.Tail().Key())
	}
}

func prepareAdaptation(t *testing.T, c *Cache[int, int], recencyBias bool) {
	c.cache.evictionMutex.Lock()
	k := -1
	if recencyBias {
		k = 1
	}
	c.cache.evictionPolicy.StepSize = float64(k) * math.Abs(c.cache.evictionPolicy.StepSize)
	maximum := c.cache.evictionPolicy.Maximum
	c.cache.evictionPolicy.WindowMaximum = uint64(0.5 * float64(maximum))
	c.cache.evictionPolicy.MainProtectedMaximum = uint64(tinylfu.PercentMainProtected * float64(maximum-c.cache.evictionPolicy.WindowMaximum))
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
	c.cache.evictionPolicy.PreviousSampleHitRate = 0.8
	c.cache.evictionPolicy.MissesInSample = sampleSize / 2
	c.cache.evictionPolicy.HitsInSample = sampleSize - c.cache.evictionPolicy.MissesInSample
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
		c.cache.evictionPolicy.MainProtectedMaximum = 0
		c.cache.evictionPolicy.WindowMaximum = maximum
		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)
		expected := make([]int, 0, maximum)
		c.cache.evictionMutex.Lock()
		h := c.cache.evictionPolicy.Window.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		c.cache.evictionPolicy.WindowMaximum = 0
		candidate := c.cache.evictionPolicy.EvictFromWindow()
		require.False(t, node.Equals(candidate, nil))

		actual := make([]int, 0, maximum)
		h = c.cache.evictionPolicy.Probation.Head()
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
		c.cache.evictionPolicy.WindowMaximum = 0
		candidate := c.cache.evictionPolicy.EvictFromWindow()
		require.False(t, node.Equals(candidate, nil))

		expected := make([]int, 0, maximum)
		h := c.cache.evictionPolicy.Probation.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		h = c.cache.evictionPolicy.Protected.Head()
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

		c.cache.evictionPolicy.WindowMaximum = maximum / 2
		c.cache.evictionPolicy.MainProtectedMaximum = 0

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		for i := range c.cache.evictionPolicy.Sketch.Table {
			c.cache.evictionPolicy.Sketch.Table[i] = 0
		}

		expected := make([]int, 0, maximum)
		h := c.cache.evictionPolicy.Window.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}

		c.cache.evictionPolicy.Maximum = maximum / 2
		c.cache.evictionPolicy.WindowMaximum = 0
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

		c.cache.evictionPolicy.WindowMaximum = maximum / 2
		c.cache.evictionPolicy.MainProtectedMaximum = 0

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		for i := range c.cache.evictionPolicy.Sketch.Table {
			c.cache.evictionPolicy.Sketch.Table[i] = 0
		}

		expected := make([]int, 0, maximum)
		h := c.cache.evictionPolicy.Window.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}

		c.cache.evictionPolicy.Maximum = maximum / 2
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

		e.WindowMaximum = maximum / 2
		e.MainProtectedMaximum = maximum / 2

		for i := 0; i < maximum; i++ {
			v, ok := c.Set(i, i)
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		cleanup(c)

		for !e.Probation.IsEmpty() {
			n := e.Probation.PopFront()
			e.Protected.PushBack(n)
			n.MakeMainProtected()
		}
		for i := range c.cache.evictionPolicy.Sketch.Table {
			c.cache.evictionPolicy.Sketch.Table[i] = 0
		}
		e.MainProtectedWeightedSize = maximum - e.WindowWeightedSize

		expected := make([]int, 0, maximum)
		h := e.Window.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		h = e.Probation.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		h = e.Protected.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}

		e.WindowMaximum = 0
		e.MainProtectedMaximum = 0
		e.Maximum = 0
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

		for i := 0; i < len(c.cache.evictionPolicy.Sketch.Table); i++ {
			e.Sketch.Table[i] = 0
		}

		expected := make([]int, 0, maximum)
		h := e.Window.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		h = e.Probation.Head()
		for !node.Equals(h, nil) {
			expected = append(expected, h.Key())
			h = h.Next()
		}
		h = e.Protected.Head()
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
		expected := e.Window.Head()
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

		e.WindowMaximum--
		e.Maximum--
		c.cache.evictNodes()

		require.True(t, expected.IsDead())
		require.Equal(t, e.Maximum, uint64(c.EstimatedSize()))
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
		expected := e.Probation.Head()
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

		e.WindowMaximum--
		e.Maximum--
		c.cache.evictNodes()

		require.True(t, expected.IsDead())
		require.Equal(t, e.Maximum, uint64(c.EstimatedSize()))
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

		candidate := e.Window.Head()
		c.SetMaximum(0)
		c.cache.evictNodes()

		require.True(t, c.has(candidate.Key()))
		require.Equal(t, uint64(0), c.WeightedSize())
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

		e.Sketch.EnsureCapacity(maximum)
		candidate := 0
		victim := 1

		require.False(t, e.Admit(candidate, victim))
		e.Sketch.Increment(candidate)
		require.True(t, e.Admit(candidate, victim))

		for i := 0; i < 15; i++ {
			e.Sketch.Increment(victim)
			require.False(t, e.Admit(candidate, victim))
		}

		for e.Sketch.Frequency(candidate) < tinylfu.AdmitHashdosThreshold {
			e.Sketch.Increment(candidate)
		}

		allow := 0
		rejected := 0
		count := uint32(3859390116)
		e.Rand = func() uint32 {
			c := count
			count++
			return c
		}
		for i := 0; i < 1000; i++ {
			if e.Admit(candidate, victim) {
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
		sampleSize := e.Sketch.SampleSize
		protectedSize := e.MainProtectedWeightedSize
		protectedMaximum := e.MainProtectedMaximum
		windowSize := e.WindowWeightedSize
		windowMaximum := e.WindowMaximum
		c.cache.evictionMutex.Unlock()

		adapt(t, c, sampleSize)

		c.cache.evictionMutex.Lock()
		require.Less(t, e.MainProtectedWeightedSize, protectedSize)
		require.Less(t, e.MainProtectedMaximum, protectedMaximum)
		require.Greater(t, e.WindowWeightedSize, windowSize)
		require.Greater(t, e.WindowMaximum, windowMaximum)
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
		sampleSize := e.Sketch.SampleSize
		protectedSize := e.MainProtectedWeightedSize
		protectedMaximum := e.MainProtectedMaximum
		windowSize := e.WindowWeightedSize
		windowMaximum := e.WindowMaximum
		c.cache.evictionMutex.Unlock()

		adapt(t, c, sampleSize)

		c.cache.evictionMutex.Lock()
		require.Greater(t, e.MainProtectedWeightedSize, protectedSize)
		require.Greater(t, e.MainProtectedMaximum, protectedMaximum)
		require.Less(t, e.WindowWeightedSize, windowSize)
		require.Less(t, e.WindowMaximum, windowMaximum)
		c.cache.evictionMutex.Unlock()
	})
}
