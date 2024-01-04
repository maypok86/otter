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

	"github.com/maypok86/otter/internal/xruntime"
)

func TestCache_Set(t *testing.T) {
	b, err := NewBuilder[int, int](100)
	if err != nil {
		t.Fatalf("can not create builder: %v", err)
	}

	c, err := b.StatsEnabled(true).Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

	for i := 0; i < 100; i++ {
		c.SetWithTTL(i, i, time.Minute)
	}

	parallelism := xruntime.Parallelism()
	var wg sync.WaitGroup
	for i := 0; i < int(parallelism); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			r := rand.New(rand.NewSource(time.Now().UnixNano()))
			for a := 0; a < 10000; a++ {
				k := r.Int() % 100
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
	ratio := c.Ratio()
	if ratio != 1.0 {
		t.Fatalf("cache hit ratio should be 1.0, but got %v", ratio)
	}
}

func TestCache_SetWithTTL(t *testing.T) {
	size := 256
	c, err := MustBuilder[int, int](size).Build()
	if err != nil {
		t.Fatalf("can not create builder: %v", err)
	}

	for i := 0; i < size; i++ {
		c.SetWithTTL(i, i, time.Second)
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

	c, err = MustBuilder[int, int](size).Build()
	if err != nil {
		t.Fatalf("can not create builder: %v", err)
	}

	for i := 0; i < size; i++ {
		c.SetWithTTL(i, i, 5*time.Second)
	}

	time.Sleep(7 * time.Second)

	for i := 0; i < size; i++ {
		if c.Has(i) {
			t.Fatalf("key should be expired: %d", i)
		}
	}

	time.Sleep(10 * time.Millisecond)

	if cacheSize := c.Size(); cacheSize != 0 {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, 0)
	}
}

func TestCache_Ratio(t *testing.T) {
	b, err := NewBuilder[uint64, uint64](100)
	if err != nil {
		t.Fatalf("can not create builder: %v", err)
	}

	c, err := b.StatsEnabled(true).Build()
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

	t.Logf("actual size: %d, capacity: %d", c.Size(), c.Capacity())
	t.Logf("actual: %.2f, optimal: %.2f", c.Ratio(), o.Ratio())
}

func TestCache_Close(t *testing.T) {
	size := 10
	c, err := MustBuilder[int, int](size).Build()
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
	if !c.isClosed {
		t.Fatalf("cache should be closed")
	}

	c.Close()

	if cacheSize := c.Size(); cacheSize != 0 {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, 0)
	}
	if !c.isClosed {
		t.Fatalf("cache should be closed")
	}
}

func TestCache_Clear(t *testing.T) {
	size := 10
	c, err := MustBuilder[int, int](size).Build()
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
	if c.isClosed {
		t.Fatalf("cache shouldn't be closed")
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
