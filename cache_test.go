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
	statsCounter := stats.NewCounter()
	m := make(map[DeletionCause]int)
	mutex := sync.Mutex{}
	c, err := NewBuilder[int, int]().
		OnDeletion(func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		}).
		RecordStats(statsCounter).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

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
	size := 10
	pinned := 4
	m := make(map[DeletionCause]int)
	mutex := sync.Mutex{}
	c, err := NewBuilder[int, int]().
		MaximumWeight(uint64(size)).
		Weigher(func(key int, value int) uint32 {
			if key == pinned {
				return 0
			}
			return 1
		}).
		WithTTL(2 * time.Second).
		OnDeletion(func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		}).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

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
	statsCounter := stats.NewCounter()
	size := uint64(10)
	c, err := NewBuilder[uint32, int]().
		MaximumWeight(size).
		Weigher(func(key uint32, value int) uint32 {
			return key
		}).
		RecordStats(statsCounter).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

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
	size := 10
	ttl := time.Hour
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithTTL(ttl).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

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
	size := 10
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

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
	size := 10
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

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
	size := getRandomSize(t)
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	statsCounter := stats.NewCounter()
	done := make(chan struct{})
	count := 0
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithTTL(time.Minute).
		RecordStats(statsCounter).
		OnDeletion(func(e DeletionEvent[int, int]) {
			mutex.Lock()
			count++
			m[e.Cause]++
			if count == size {
				done <- struct{}{}
			}
			mutex.Unlock()
		}).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	// update
	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	parallelism := xruntime.Parallelism()
	var wg sync.WaitGroup
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
	size := getRandomSize(t)
	statsCounter := stats.NewCounter()
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithTTL(time.Hour).
		RecordStats(statsCounter).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

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

	cc, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithVariableTTL().
		RecordStats(statsCounter).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

	for i := 0; i < size; i++ {
		if _, ok := cc.SetIfAbsentWithTTL(i, i, time.Hour); !ok {
			t.Fatalf("set was dropped. key: %d", i)
		}
	}

	for i := 0; i < size; i++ {
		if !cc.Has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
	}

	for i := 0; i < size; i++ {
		if _, ok := cc.SetIfAbsentWithTTL(i, i, time.Second); ok {
			t.Fatalf("set wasn't dropped. key: %d", i)
		}
	}

	if hitRatio := statsCounter.Snapshot().HitRatio(); hitRatio != 1.0 {
		t.Fatalf("hit rate should be 100%%. Hite rate: %.2f", hitRatio*100)
	}

	cc.Close()
}

func TestCache_SetWithTTL(t *testing.T) {
	size := getRandomSize(t)
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	statsCounter := stats.NewCounter()
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		InitialCapacity(size).
		RecordStats(statsCounter).
		WithTTL(time.Second).
		OnDeletion(func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		}).
		Build()
	if err != nil {
		t.Fatalf("can not create builder: %v", err)
	}

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
	cc, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithVariableTTL().
		RecordStats(statsCounter).
		OnDeletion(func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		}).
		Build()
	if err != nil {
		t.Fatalf("can not create builder: %v", err)
	}

	for i := 0; i < size; i++ {
		cc.SetWithTTL(i, i, 5*time.Second)
	}

	time.Sleep(7 * time.Second)

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
	if len(m) != 1 || m[CauseExpiration] != size {
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
	size := getRandomSize(t)
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		InitialCapacity(size).
		WithTTL(time.Hour).
		OnDeletion(func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		}).
		Build()
	if err != nil {
		t.Fatalf("can not create builder: %v", err)
	}

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
	size := getRandomSize(t)
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		InitialCapacity(size).
		WithTTL(time.Hour).
		OnDeletion(func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		}).
		Build()
	if err != nil {
		t.Fatalf("can not create builder: %v", err)
	}

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

	cache, err := NewBuilder[string, string]().
		MaximumSize(1000).
		WithTTL(time.Hour).
		Build()
	if err != nil {
		panic(err)
	}

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
					cache.InvalidateAll()
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

func TestCache_Extension(t *testing.T) {
	size := getRandomSize(t)
	defaultTTL := time.Hour
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithTTL(defaultTTL).
		Build()
	if err != nil {
		t.Fatalf("can not create builder: %v", err)
	}

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	k1 := 4
	v1, ok := c.Extension().GetQuietly(k1)
	if !ok {
		t.Fatalf("not found key %d", k1)
	}

	e1, ok := c.Extension().GetEntryQuietly(k1)
	if !ok {
		t.Fatalf("not found key %d", k1)
	}

	e2, ok := c.Extension().GetEntry(k1)
	if !ok {
		t.Fatalf("not found key %d", k1)
	}

	time.Sleep(time.Second)

	isValidEntries := e1.Key() == k1 &&
		e1.Value() == v1 &&
		e1.Weight() == 1 &&
		e1 == e2 &&
		e1.TTL() < defaultTTL &&
		!e1.HasExpired()

	if !isValidEntries {
		t.Fatalf("found not valid entries. e1: %+v, e2: %+v, v1:%d", e1, e2, v1)
	}

	if _, ok := c.Extension().GetQuietly(size); ok {
		t.Fatalf("found not valid key: %d", size)
	}
	if _, ok := c.Extension().GetEntryQuietly(size); ok {
		t.Fatalf("found not valid key: %d", size)
	}
	if _, ok := c.Extension().GetEntry(size); ok {
		t.Fatalf("found not valid key: %d", size)
	}
}

func TestCache_Ratio(t *testing.T) {
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	statsCounter := stats.NewCounter()
	capacity := 100
	c, err := NewBuilder[uint64, uint64]().
		MaximumSize(capacity).
		RecordStats(statsCounter).
		OnDeletion(func(e DeletionEvent[uint64, uint64]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		}).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

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
	done := make(chan struct{})

	c, err := NewBuilder[string, string]().
		RecordStats(stats.NewCounter()).
		OnDeletion(func(e DeletionEvent[string, string]) {
			defer func() {
				done <- struct{}{}
			}()

			if e.Cause != CauseExpiration {
				t.Fatalf("err not expired: %v", e.Cause)
			}
		}).
		WithTTL(3 * time.Second).
		Build()
	if err != nil {
		t.Fatal(err)
	}

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
