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
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maypok86/otter/v2/core"
	"github.com/maypok86/otter/v2/core/expiry"
	"github.com/maypok86/otter/v2/core/stats"
	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/xruntime"
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

func TestCache_Unbounded(t *testing.T) {
	t.Parallel()

	statsCounter := stats.NewCounter()
	m := make(map[DeletionCause]int)
	mutex := sync.Mutex{}
	c := Must[int, int](&Options[int, int]{
		StatsRecorder: statsCounter,
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		},
	})

	size := getRandomSize(t)
	for i := 0; i < size; i++ {
		c.Set(i, i)
	}
	for i := 0; i < size; i++ {
		if !c.Has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
	}
	for i := size; i < 2*size; i++ {
		if c.Has(i) {
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
	c := Must[int, int](&Options[int, int]{
		MaximumWeight: uint64(size),
		Weigher: func(key int, value int) uint32 {
			if key == pinned {
				return 0
			}
			return 1
		},
		ExpiryCalculator: expiry.Writing[int, int](2 * time.Second),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}
	for i := 0; i < size; i++ {
		if !c.Has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
		c.Has(i)
	}
	for i := size; i < 2*size; i++ {
		c.Set(i, i)
		if !c.Has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
		c.Has(i)
	}
	if !c.Has(pinned) {
		t.Fatalf("the key must exist: %d", pinned)
	}

	time.Sleep(4 * time.Second)

	if c.Has(pinned) {
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
		MaximumWeight: size,
		Weigher: func(key uint32, value int) uint32 {
			return key
		},
		StatsRecorder: statsCounter,
	})

	goodWeight1 := 1
	goodWeight2 := 2
	badWeight := 8

	c.Set(uint32(goodWeight1), 1)
	c.Set(uint32(goodWeight2), 1)
	c.Set(uint32(badWeight), 1)
	time.Sleep(time.Second)
	if !c.Has(uint32(goodWeight1)) {
		t.Fatalf("the key must exist: %d", goodWeight1)
	}
	if !c.Has(uint32(goodWeight2)) {
		t.Fatalf("the key must exist: %d", goodWeight2)
	}
	if c.Has(uint32(badWeight)) {
		t.Fatalf("the key must not exist: %d", badWeight)
	}
}

func TestCache_Range(t *testing.T) {
	t.Parallel()

	size := 10
	ttl := time.Hour
	c := Must[int, int](&Options[int, int]{
		MaximumSize:      size,
		ExpiryCalculator: expiry.Writing[int, int](ttl),
	})

	time.Sleep(3 * time.Second)

	nm := node.NewManager[int, int](node.Config{
		WithExpiration: true,
		WithWeight:     true,
	})

	c.Set(1, 1)
	c.hashmap.Compute(2, func(n node.Node[int, int]) node.Node[int, int] {
		return nm.Create(2, 2, 1, 1)
	})
	c.Set(3, 3)
	aliveNodes := 2
	iters := 0
	c.Range(func(key, value int) bool {
		if key != value {
			t.Fatalf("got unexpected key/value for iteration %d: %d/%d", iters, key, value)
			return false
		}
		iters++
		return true
	})
	if iters != aliveNodes {
		t.Fatalf("got unexpected number of iterations: %d", iters)
	}
}

func TestCache_Close(t *testing.T) {
	t.Parallel()

	size := 10
	c := Must(&Options[int, int]{
		MaximumSize: size,
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	if cacheSize := c.Size(); cacheSize != size {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, size)
	}

	c.Close()

	time.Sleep(10 * time.Millisecond)

	if cacheSize := c.Size(); cacheSize != 0 {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, 0)
	}

	c.Close()

	if cacheSize := c.Size(); cacheSize != 0 {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, 0)
	}
}

func TestCache_InvalidateAll(t *testing.T) {
	t.Parallel()

	size := 10
	c := Must(&Options[int, int]{
		MaximumSize: size,
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	if cacheSize := c.Size(); cacheSize != size {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, size)
	}

	c.InvalidateAll()

	time.Sleep(10 * time.Millisecond)

	if cacheSize := c.Size(); cacheSize != 0 {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, 0)
	}
}

func TestCache_Set(t *testing.T) {
	t.Parallel()

	size := getRandomSize(t)
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	statsCounter := stats.NewCounter()
	done := make(chan struct{})
	count := 0
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: expiry.Writing[int, int](time.Minute),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			count++
			m[e.Cause]++
			if count == size {
				done <- struct{}{}
			}
			mutex.Unlock()
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	// update
	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	parallelism := xruntime.Parallelism()
	var (
		wg  sync.WaitGroup
		err error
	)
	for i := 0; i < int(parallelism); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			r := rand.New(rand.NewSource(time.Now().UnixNano()))
			for a := 0; a < 10000; a++ {
				k := r.Int() % size
				val, ok := c.GetIfPresent(k)
				if !ok {
					err = fmt.Errorf("expected %d but got nil", k)
					break
				}
				if val != k {
					err = fmt.Errorf("expected %d but got %d", k, val)
					break
				}
			}
		}()
	}
	wg.Wait()

	if err != nil {
		t.Fatalf("not found key: %v", err)
	}
	ratio := statsCounter.Snapshot().HitRatio()
	if ratio != 1.0 {
		t.Fatalf("cache hit ratio should be 1.0, but got %v", ratio)
	}

	<-done
	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 1 || m[CauseReplacement] != size {
		t.Fatalf("cache was supposed to replace %d, but replaced %d entries", size, m[CauseReplacement])
	}
}

func TestCache_SetIfAbsent(t *testing.T) {
	t.Parallel()

	size := getRandomSize(t)
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: expiry.Writing[int, int](time.Hour),
	})

	for i := 0; i < size; i++ {
		if _, ok := c.SetIfAbsent(i, i); !ok {
			t.Fatalf("set was dropped. key: %d", i)
		}
	}

	for i := 0; i < size; i++ {
		if !c.Has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
	}

	for i := 0; i < size; i++ {
		if _, ok := c.SetIfAbsent(i, i); ok {
			t.Fatalf("set wasn't dropped. key: %d", i)
		}
	}

	c.InvalidateAll()

	if hitRatio := statsCounter.Snapshot().HitRatio(); hitRatio != 1.0 {
		t.Fatalf("hit rate should be 100%%. Hite rate: %.2f", hitRatio*100)
	}

	c.Close()
}

func TestCache_SetWithExpiresAt(t *testing.T) {
	t.Parallel()

	size := getRandomSize(t)
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		InitialCapacity:  size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: expiry.Creating[int, int](time.Second),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	time.Sleep(3 * time.Second)
	for i := 0; i < size; i++ {
		if c.Has(i) {
			t.Fatalf("key should be expired: %d", i)
		}
	}

	time.Sleep(10 * time.Millisecond)

	if cacheSize := c.Size(); cacheSize != 0 {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, 0)
	}

	mutex.Lock()
	if e := m[CauseExpiration]; len(m) != 1 || e != size {
		mutex.Unlock()
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", size, e)
	}
	if statsCounter.Snapshot().Evictions() != uint64(m[CauseExpiration]) {
		mutex.Unlock()
		t.Fatalf(
			"Eviction statistics are not collected for expiration. Evictions: %d, expired entries: %d",
			statsCounter.Snapshot().Evictions(),
			m[CauseExpiration],
		)
	}
	mutex.Unlock()

	m = make(map[DeletionCause]int)
	statsCounter = stats.NewCounter()
	if size%2 == 1 {
		size++
	}
	cc := Must(&Options[int, int]{
		MaximumSize:   size,
		StatsRecorder: statsCounter,
		ExpiryCalculator: expiry.WritingFunc(func(entry core.Entry[int, int]) time.Duration {
			if entry.Key%2 == 0 {
				return time.Second
			}
			return 4 * time.Second
		}),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		},
	})

	for i := 0; i < size; i++ {
		cc.Set(i, i)
	}

	time.Sleep(2 * time.Second)

	if c.Size() != size%2 {
		t.Fatalf("half of the keys must be expired. wantedCurrentSize %d, got %d", size%2, c.Size())
	}

	for i := 0; i < size; i++ {
		if i%2 == 0 {
			continue
		}
		cc.Set(i, i)
	}

	time.Sleep(3 * time.Second)

	for i := 0; i < size; i++ {
		if i%2 == 0 {
			continue
		}
		if !cc.Has(i) {
			t.Fatalf("key should not be expired: %d", i)
		}
	}

	time.Sleep(5 * time.Second)

	for i := 0; i < size; i++ {
		if cc.Has(i) {
			t.Fatalf("key should be expired: %d", i)
		}
	}

	time.Sleep(10 * time.Millisecond)

	if cacheSize := cc.Size(); cacheSize != 0 {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, 0)
	}
	if misses := statsCounter.Snapshot().Misses(); misses != uint64(size) {
		t.Fatalf("c.Stats().Misses() = %d, want = %d", misses, size)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 2 || m[CauseExpiration] != size && m[CauseReplacement] != size/2 {
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", size, m[CauseExpiration])
	}
	if statsCounter.Snapshot().Evictions() != uint64(m[CauseExpiration]) {
		mutex.Unlock()
		t.Fatalf(
			"Eviction statistics are not collected for expiration. Evictions: %d, expired entries: %d",
			statsCounter.Snapshot().Evictions(),
			m[CauseExpiration],
		)
	}
}

func TestCache_Invalidate(t *testing.T) {
	t.Parallel()

	size := getRandomSize(t)
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		InitialCapacity:  size,
		ExpiryCalculator: expiry.Writing[int, int](time.Hour),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	for i := 0; i < size; i++ {
		if !c.Has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
	}

	for i := 0; i < size; i++ {
		c.Invalidate(i)
	}

	for i := 0; i < size; i++ {
		if c.Has(i) {
			t.Fatalf("the key must not exist: %d", i)
		}
	}

	time.Sleep(time.Second)

	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 1 || m[CauseInvalidation] != size {
		t.Fatalf("cache was supposed to delete %d, but deleted %d entries", size, m[CauseInvalidation])
	}
}

func TestCache_InvalidateByFunc(t *testing.T) {
	t.Parallel()

	size := getRandomSize(t)
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		InitialCapacity:  size,
		ExpiryCalculator: expiry.Writing[int, int](time.Hour),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	c.InvalidateByFunc(func(key int, value int) bool {
		return key%2 == 1
	})

	c.Range(func(key int, value int) bool {
		if key%2 == 1 {
			t.Fatalf("key should be odd, but got: %d", key)
		}
		return true
	})

	time.Sleep(time.Second)

	expected := size / 2
	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 1 || m[CauseInvalidation] != expected {
		t.Fatalf("cache was supposed to delete %d, but deleted %d entries", expected, m[CauseInvalidation])
	}
}

func TestCache_ConcurrentInvalidateAll(t *testing.T) {
	t.Parallel()

	c := Must(&Options[string, string]{
		MaximumSize:      1000,
		ExpiryCalculator: expiry.Writing[string, string](time.Hour),
	})

	var success atomic.Bool
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
		success.Store(true)
	}()

	time.Sleep(2 * time.Second)

	if !success.Load() {
		t.Fatal("multiple concurrent clearing operations should not be blocked")
	}
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
		if !c.Has(k) {
			c.Set(k, k)
		}
	}

	t.Logf("actual size: %d, capacity: %d", c.Size(), capacity)
	t.Logf("actual: %.2f, optimal: %.2f", statsCounter.Snapshot().HitRatio(), o.Ratio())

	time.Sleep(2 * time.Second)

	if size := c.Size(); size != capacity {
		t.Fatalf("not valid cache size. expected %d, but got %d", capacity, size)
	}

	mutex.Lock()
	defer mutex.Unlock()
	t.Logf("evicted: %d", m[CauseOverflow])
	if len(m) != 1 || m[CauseOverflow] <= 0 || m[CauseOverflow] > 5000 {
		t.Fatalf("cache was supposed to evict positive number of entries, but evicted %d entries", m[CauseOverflow])
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

func Test_GetExpired(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})

	c := Must(&Options[string, string]{
		StatsRecorder: stats.NewCounter(),
		OnDeletion: func(e DeletionEvent[string, string]) {
			defer func() {
				done <- struct{}{}
			}()

			if e.Cause != CauseExpiration {
				t.Fatalf("err not expired: %v", e.Cause)
			}
		},
		ExpiryCalculator: expiry.Writing[string, string](3 * time.Second),
	})

	c.Set("test1", "123456")
	for i := 0; i < 5; i++ {
		c.GetIfPresent("test1")
		time.Sleep(1 * time.Second)
	}

	select {
	case <-time.After(2 * time.Second):
		t.Fatal("the entry wasn't expired")
	case <-done:
	}
}
