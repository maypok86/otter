// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
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
	"testing"
	"time"

	"github.com/maypok86/otter/v2/internal/generated/node"
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

func TestCache_Unbounded(t *testing.T) {
	statsCounter := stats.NewCounter()
	m := make(map[DeletionCause]int)
	mutex := sync.Mutex{}
	c, err := NewBuilder[int, int]().
		DeletionListener(func(key int, value int, cause DeletionCause) {
			mutex.Lock()
			m[cause]++
			mutex.Unlock()
		}).
		CollectStats(statsCounter).
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
		c.Delete(i)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 2 || m[Explicit] != size-replaced {
		t.Fatalf("cache was supposed to delete %d, but deleted %d entries", size-replaced, m[Explicit])
	}
	if m[Replaced] != replaced {
		t.Fatalf("cache was supposed to replace %d, but replaced %d entries", replaced, m[Replaced])
	}
}

func TestCache_SetWithWeight(t *testing.T) {
	size := uint64(10)
	c, err := NewBuilder[uint32, int]().
		MaximumWeight(size).
		Weigher(func(key uint32, value int) uint32 {
			return key
		}).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

	goodWeight := c.policy.MaxAvailableWeight()
	badWeight := goodWeight + 1

	added := c.Set(uint32(goodWeight), 1)
	if !added {
		t.Fatalf("Set was dropped, even though it shouldn't have been. Max available weight: %d, actual weight: %d",
			c.policy.MaxAvailableWeight(),
			c.weigher(uint32(goodWeight), 1),
		)
	}
	added = c.Set(uint32(badWeight), 1)
	if added {
		t.Fatalf("Set wasn't dropped, though it should have been. Max available weight: %d, actual weight: %d",
			c.policy.MaxAvailableWeight(),
			c.weigher(uint32(badWeight), 1),
		)
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
	c.hashmap.Set(nm.Create(2, 2, 1, 1))
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

func TestCache_Clear(t *testing.T) {
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

	c.Clear()

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
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithTTL(time.Minute).
		CollectStats(statsCounter).
		DeletionListener(func(key int, value int, cause DeletionCause) {
			mutex.Lock()
			m[cause]++
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
				val, ok := c.Get(k)
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

	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 1 || m[Replaced] != size {
		t.Fatalf("cache was supposed to replace %d, but replaced %d entries", size, m[Replaced])
	}
}

func TestCache_SetIfAbsent(t *testing.T) {
	size := getRandomSize(t)
	statsCounter := stats.NewCounter()
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithTTL(time.Hour).
		CollectStats(statsCounter).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

	for i := 0; i < size; i++ {
		if !c.SetIfAbsent(i, i) {
			t.Fatalf("set was dropped. key: %d", i)
		}
	}

	for i := 0; i < size; i++ {
		if !c.Has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
	}

	for i := 0; i < size; i++ {
		if c.SetIfAbsent(i, i) {
			t.Fatalf("set wasn't dropped. key: %d", i)
		}
	}

	c.Clear()

	cc, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithVariableTTL().
		CollectStats(statsCounter).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

	for i := 0; i < size; i++ {
		if !cc.SetIfAbsentWithTTL(i, i, time.Hour) {
			t.Fatalf("set was dropped. key: %d", i)
		}
	}

	for i := 0; i < size; i++ {
		if !cc.Has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
	}

	for i := 0; i < size; i++ {
		if cc.SetIfAbsentWithTTL(i, i, time.Second) {
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
		CollectStats(statsCounter).
		WithTTL(time.Second).
		DeletionListener(func(key int, value int, cause DeletionCause) {
			mutex.Lock()
			m[cause]++
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
	if e := m[Expired]; len(m) != 1 || e != size {
		mutex.Unlock()
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", size, e)
	}
	if statsCounter.Snapshot().Evictions() != uint64(m[Expired]) {
		mutex.Unlock()
		t.Fatalf(
			"Eviction statistics are not collected for expiration. Evictions: %d, expired entries: %d",
			statsCounter.Snapshot().Evictions(),
			m[Expired],
		)
	}
	mutex.Unlock()

	m = make(map[DeletionCause]int)
	statsCounter = stats.NewCounter()
	cc, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithVariableTTL().
		CollectStats(statsCounter).
		DeletionListener(func(key int, value int, cause DeletionCause) {
			mutex.Lock()
			m[cause]++
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
	if len(m) != 1 || m[Expired] != size {
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", size, m[Expired])
	}
	if statsCounter.Snapshot().Evictions() != uint64(m[Expired]) {
		mutex.Unlock()
		t.Fatalf(
			"Eviction statistics are not collected for expiration. Evictions: %d, expired entries: %d",
			statsCounter.Snapshot().Evictions(),
			m[Expired],
		)
	}
}

func TestCache_Delete(t *testing.T) {
	size := getRandomSize(t)
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		InitialCapacity(size).
		WithTTL(time.Hour).
		DeletionListener(func(key int, value int, cause DeletionCause) {
			mutex.Lock()
			m[cause]++
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
		c.Delete(i)
	}

	for i := 0; i < size; i++ {
		if c.Has(i) {
			t.Fatalf("the key must not exist: %d", i)
		}
	}

	time.Sleep(time.Second)

	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 1 || m[Explicit] != size {
		t.Fatalf("cache was supposed to delete %d, but deleted %d entries", size, m[Explicit])
	}
}

func TestCache_DeleteByFunc(t *testing.T) {
	size := getRandomSize(t)
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		InitialCapacity(size).
		WithTTL(time.Hour).
		DeletionListener(func(key int, value int, cause DeletionCause) {
			mutex.Lock()
			m[cause]++
			mutex.Unlock()
		}).
		Build()
	if err != nil {
		t.Fatalf("can not create builder: %v", err)
	}

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	c.DeleteByFunc(func(key int, value int) bool {
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
	if len(m) != 1 || m[Explicit] != expected {
		t.Fatalf("cache was supposed to delete %d, but deleted %d entries", expected, m[Explicit])
	}
}

func TestCache_Advanced(t *testing.T) {
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
		CollectStats(statsCounter).
		DeletionListener(func(key uint64, value uint64, cause DeletionCause) {
			mutex.Lock()
			m[cause]++
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

	mutex.Lock()
	defer mutex.Unlock()
	t.Logf("evicted: %d", m[Size])
	if len(m) != 1 || m[Size] <= 0 || m[Size] > 5000 {
		t.Fatalf("cache was supposed to evict positive number of entries, but evicted %d entries", m[Size])
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
	c, err := NewBuilder[string, string]().
		CollectStats(stats.NewCounter()).
		DeletionListener(func(key string, value string, cause DeletionCause) {
			fmt.Println(cause)
			if cause != Expired {
				t.Fatalf("err not expired: %v", cause)
			}
		}).
		WithVariableTTL().
		Build()
	if err != nil {
		t.Fatal(err)
	}
	c.SetWithTTL("test1", "123456", time.Duration(12)*time.Second)
	for i := 0; i < 5; i++ {
		c.Get("test1")
		time.Sleep(3 * time.Second)
	}
	time.Sleep(1 * time.Second)
}
