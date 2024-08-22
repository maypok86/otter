// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
// Copyright (c) 2021 Andrey Pechkurov
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
//
// Copyright notice. This code is a fork of tests for xsync.MapOf from this file with some changes:
// https://github.com/puzpuzpuz/xsync/blob/main/mapof_test.go
//
// Use of this source code is governed by a MIT license that can be found
// at https://github.com/puzpuzpuz/xsync/blob/main/LICENSE

package hashtable

import (
	"math/rand"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

func TestMap_PaddedBucketSize(t *testing.T) {
	size := unsafe.Sizeof(paddedBucket{})
	if size != xruntime.CacheLineSize {
		t.Fatalf("size of 64B (one cache line) is expected, got: %d", size)
	}
}

func TestMap_EmptyStringKey(t *testing.T) {
	nm := node.NewManager[string, string](node.Config{})
	m := New(nm)
	m.Set(nm.Create("", "foobar", 0, 1))
	n, ok := m.Get("")
	if !ok {
		t.Fatal("value was expected")
	}
	if n.Value() != "foobar" {
		t.Fatalf("value does not match: %v", n.Value())
	}
}

func TestMap_SetNilValue(t *testing.T) {
	nm := node.NewManager[string, *struct{}](node.Config{})
	m := New(nm)
	m.Set(nm.Create("foo", nil, 0, 1))
	n, ok := m.Get("foo")
	if !ok {
		t.Fatal("nil value was expected")
	}
	if n.Value() != nil {
		t.Fatalf("value was not nil: %v", n.Value())
	}
}

func TestMap_Set(t *testing.T) {
	const numberOfNodes = 128
	nm := node.NewManager[string, int](node.Config{})
	m := New(nm)
	for i := 0; i < numberOfNodes; i++ {
		m.Set(nm.Create(strconv.Itoa(i), i, 0, 1))
	}
	for i := 0; i < numberOfNodes; i++ {
		n, ok := m.Get(strconv.Itoa(i))
		if !ok {
			t.Fatalf("value not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n.Value())
		}
	}
}

func TestMap_SetIfAbsent(t *testing.T) {
	const numberOfNodes = 128
	nm := node.NewManager[string, int](node.Config{})
	m := New(nm)
	for i := 0; i < numberOfNodes; i++ {
		res := m.SetIfAbsent(nm.Create(strconv.Itoa(i), i, 0, 1))
		if res != nil {
			t.Fatalf("set was dropped. got: %+v", res)
		}
	}
	for i := 0; i < numberOfNodes; i++ {
		n := nm.Create(strconv.Itoa(i), i, 0, 1)
		res := m.SetIfAbsent(n)
		if res == nil {
			t.Fatalf("set was not dropped. node that was set: %+v", res)
		}
	}

	for i := 0; i < numberOfNodes; i++ {
		n, ok := m.Get(strconv.Itoa(i))
		if !ok {
			t.Fatalf("value not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n.Value())
		}
	}
}

// this code may break if the maphash.Hasher[k] structure changes.
type hasher struct {
	hash func(pointer unsafe.Pointer, seed uintptr) uintptr
	seed uintptr
}

func TestMap_SetWithCollisions(t *testing.T) {
	const numNodes = 1000
	nm := node.NewManager[int, int](node.Config{})
	m := NewWithSize(nm, numNodes)
	table := (*table[int])(atomic.LoadPointer(&m.table))
	hasher := (*hasher)((unsafe.Pointer)(&table.hasher))
	hasher.hash = func(ptr unsafe.Pointer, seed uintptr) uintptr {
		// We intentionally use an awful hash function here to make sure
		// that the map copes with key collisions.
		return 42
	}
	for i := 0; i < numNodes; i++ {
		m.Set(nm.Create(i, i, 0, 1))
	}
	for i := 0; i < numNodes; i++ {
		v, ok := m.Get(i)
		if !ok {
			t.Fatalf("value not found for %d", i)
		}
		if v.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, v)
		}
	}
}

func TestMap_SetThenDelete(t *testing.T) {
	const numberOfNodes = 1000
	nm := node.NewManager[string, int](node.Config{})
	m := New(nm)
	for i := 0; i < numberOfNodes; i++ {
		m.Set(nm.Create(strconv.Itoa(i), i, 0, 1))
	}
	for i := 0; i < numberOfNodes; i++ {
		m.Delete(strconv.Itoa(i))
		if _, ok := m.Get(strconv.Itoa(i)); ok {
			t.Fatalf("value was not expected for %d", i)
		}
	}
}

func TestMap_Range(t *testing.T) {
	const numNodes = 1000
	nm := node.NewManager[string, int](node.Config{})
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		m.Set(nm.Create(strconv.Itoa(i), i, 0, 1))
	}
	iters := 0
	met := make(map[string]int)
	m.Range(func(n node.Node[string, int]) bool {
		if n.Key() != strconv.Itoa(n.Value()) {
			t.Fatalf("got unexpected key/value for iteration %d: %v/%v", iters, n.Key(), n.Value())
			return false
		}
		met[n.Key()] += 1
		iters++
		return true
	})
	if iters != numNodes {
		t.Fatalf("got unexpected number of iterations: %d", iters)
	}
	for i := 0; i < numNodes; i++ {
		if c := met[strconv.Itoa(i)]; c != 1 {
			t.Fatalf("range did not iterate correctly over %d: %d", i, c)
		}
	}
}

func TestMap_RangeFalseReturned(t *testing.T) {
	nm := node.NewManager[string, int](node.Config{})
	m := New(nm)
	for i := 0; i < 100; i++ {
		m.Set(nm.Create(strconv.Itoa(i), i, 0, 1))
	}
	iters := 0
	m.Range(func(n node.Node[string, int]) bool {
		iters++
		return iters != 13
	})
	if iters != 13 {
		t.Fatalf("got unexpected number of iterations: %d", iters)
	}
}

func TestMap_RangeNestedDelete(t *testing.T) {
	const numNodes = 256
	nm := node.NewManager[string, int](node.Config{})
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		m.Set(nm.Create(strconv.Itoa(i), i, 0, 1))
	}
	m.Range(func(n node.Node[string, int]) bool {
		m.Delete(n.Key())
		return true
	})
	for i := 0; i < numNodes; i++ {
		if _, ok := m.Get(strconv.Itoa(i)); ok {
			t.Fatalf("value found for %d", i)
		}
	}
}

func TestMap_Size(t *testing.T) {
	const numberOfNodes = 1000
	nm := node.NewManager[string, int](node.Config{})
	m := New(nm)
	size := m.Size()
	if size != 0 {
		t.Fatalf("zero size expected: %d", size)
	}
	expectedSize := 0
	for i := 0; i < numberOfNodes; i++ {
		m.Set(nm.Create(strconv.Itoa(i), i, 0, 1))
		expectedSize++
		size := m.Size()
		if size != expectedSize {
			t.Fatalf("size of %d was expected, got: %d", expectedSize, size)
		}
	}
	for i := 0; i < numberOfNodes; i++ {
		m.Delete(strconv.Itoa(i))
		expectedSize--
		size := m.Size()
		if size != expectedSize {
			t.Fatalf("size of %d was expected, got: %d", expectedSize, size)
		}
	}
}

func TestMap_Clear(t *testing.T) {
	const numberOfNodes = 1000
	nm := node.NewManager[string, int](node.Config{})
	m := New(nm)
	for i := 0; i < numberOfNodes; i++ {
		m.Set(nm.Create(strconv.Itoa(i), i, 0, 1))
	}
	size := m.Size()
	if size != numberOfNodes {
		t.Fatalf("size of %d was expected, got: %d", numberOfNodes, size)
	}
	m.Clear()
	size = m.Size()
	if size != 0 {
		t.Fatalf("zero size was expected, got: %d", size)
	}
}

func parallelSeqSetter(t *testing.T, m *Map[string, int], storers, iterations, nodes int, wg *sync.WaitGroup) {
	t.Helper()

	for i := 0; i < iterations; i++ {
		for j := 0; j < nodes; j++ {
			if storers == 0 || j%storers == 0 {
				m.Set(m.nodeManager.Create(strconv.Itoa(j), j, 0, 1))
				n, ok := m.Get(strconv.Itoa(j))
				if !ok {
					t.Errorf("value was not found for %d", j)
					break
				}
				if n.Value() != j {
					t.Errorf("value was not expected for %d: %d", j, n.Value())
					break
				}
			}
		}
	}
	wg.Done()
}

func TestMap_ParallelSets(t *testing.T) {
	const storers = 4
	const iterations = 10_000
	const nodes = 100
	nm := node.NewManager[string, int](node.Config{})
	m := New(nm)

	wg := &sync.WaitGroup{}
	wg.Add(storers)
	for i := 0; i < storers; i++ {
		go parallelSeqSetter(t, m, i, iterations, nodes, wg)
	}
	wg.Wait()

	for i := 0; i < nodes; i++ {
		n, ok := m.Get(strconv.Itoa(i))
		if !ok {
			t.Fatalf("value not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n.Value())
		}
	}
}

func parallelRandSetter(t *testing.T, m *Map[string, int], iteratinos, nodes int, wg *sync.WaitGroup) {
	t.Helper()

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < iteratinos; i++ {
		j := r.Intn(nodes)
		m.Set(m.nodeManager.Create(strconv.Itoa(j), j, 0, 1))
	}
	wg.Done()
}

func parallelRandDeleter(t *testing.T, m *Map[string, int], iterations, nodes int, wg *sync.WaitGroup) {
	t.Helper()

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < iterations; i++ {
		j := r.Intn(nodes)
		if v := m.Delete(strconv.Itoa(j)); v != nil && v.Value() != j {
			t.Errorf("value was not expected for %d: %d", j, v.Value())
		}
	}
	wg.Done()
}

func parallelGetter(t *testing.T, m *Map[string, int], iterations, nodes int, wg *sync.WaitGroup) {
	t.Helper()

	for i := 0; i < iterations; i++ {
		for j := 0; j < nodes; j++ {
			if n, ok := m.Get(strconv.Itoa(j)); ok && n.Value() != j {
				t.Errorf("value was not expected for %d: %d", j, n.Value())
			}
		}
	}
	wg.Done()
}

func TestMap_ParallelGet(t *testing.T) {
	const iterations = 100_000
	const nodes = 100
	nm := node.NewManager[string, int](node.Config{})
	m := New(nm)

	wg := &sync.WaitGroup{}
	wg.Add(3)
	go parallelRandSetter(t, m, iterations, nodes, wg)
	go parallelRandDeleter(t, m, iterations, nodes, wg)
	go parallelGetter(t, m, iterations, nodes, wg)

	wg.Wait()
}

func TestMap_ParallelSetsAndDeletes(t *testing.T) {
	const workers = 2
	const iterations = 100_000
	const nodes = 1000
	nm := node.NewManager[string, int](node.Config{})
	m := New(nm)
	wg := &sync.WaitGroup{}
	wg.Add(2 * workers)
	for i := 0; i < workers; i++ {
		go parallelRandSetter(t, m, iterations, nodes, wg)
		go parallelRandDeleter(t, m, iterations, nodes, wg)
	}

	wg.Wait()
}

func parallelTypedRangeSetter(t *testing.T, m *Map[int, int], numNodes int, stopFlag *int64, cdone chan bool) {
	t.Helper()

	for {
		for i := 0; i < numNodes; i++ {
			m.Set(m.nodeManager.Create(i, i, 0, 1))
		}
		if atomic.LoadInt64(stopFlag) != 0 {
			break
		}
	}
	cdone <- true
}

func parallelTypedRangeDeleter(t *testing.T, m *Map[int, int], numNodes int, stopFlag *int64, cdone chan bool) {
	t.Helper()

	for {
		for i := 0; i < numNodes; i++ {
			m.Delete(i)
		}
		if atomic.LoadInt64(stopFlag) != 0 {
			break
		}
	}
	cdone <- true
}

func TestMap_ParallelRange(t *testing.T) {
	const numNodes = 10_000
	nm := node.NewManager[int, int](node.Config{})
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		m.Set(nm.Create(i, i, 0, 1))
	}
	// Start goroutines that would be storing and deleting items in parallel.
	cdone := make(chan bool)
	stopFlag := int64(0)
	go parallelTypedRangeSetter(t, m, numNodes, &stopFlag, cdone)
	go parallelTypedRangeDeleter(t, m, numNodes, &stopFlag, cdone)
	// Iterate the map and verify that no duplicate keys were met.
	met := make(map[int]int)
	m.Range(func(n node.Node[int, int]) bool {
		if n.Key() != n.Value() {
			t.Fatalf("got unexpected value for key %d: %d", n.Key(), n.Value())
			return false
		}
		met[n.Key()] += 1
		return true
	})
	if len(met) == 0 {
		t.Fatal("no nodes were met when iterating")
	}
	for k, c := range met {
		if c != 1 {
			t.Fatalf("met key %d multiple times: %d", k, c)
		}
	}
	// Make sure that both goroutines finish.
	atomic.StoreInt64(&stopFlag, 1)
	<-cdone
	<-cdone
}
