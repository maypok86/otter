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

package hashmap

import (
	"math/rand"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

func testNodeManager[K comparable, V any]() *node.Manager[K, V] {
	return node.NewManager[K, V](node.Config{})
}

type point struct {
	x int32
	y int32
}

func TestMap_BucketStructSize(t *testing.T) {
	size := unsafe.Sizeof(bucketPadded{})
	if size != 64 {
		t.Fatalf("size of 64B (one cache line) is expected, got: %d", size)
	}
}

func TestMap_MissingNode(t *testing.T) {
	nm := testNodeManager[string, string]()
	m := New(nm)
	n, ok := m.Get("foo")
	if ok {
		t.Fatalf("node was not expected: %v", n)
	}
	var oldNode node.Node[string, string]
	m.Compute("foo", func(n node.Node[string, string]) node.Node[string, string] {
		oldNode = n
		return nil
	})
	if oldNode != nil {
		t.Fatalf("node was not expected %v", oldNode)
	}
	m.Compute("foo", func(n node.Node[string, string]) node.Node[string, string] {
		oldNode = n
		return nil
	})
}

func TestMapOf_EmptyStringKey(t *testing.T) {
	nm := testNodeManager[string, string]()
	m := New(nm)
	m.Compute("", func(n node.Node[string, string]) node.Node[string, string] {
		return nm.Create("", "foobar", 0, 1)
	})
	n, ok := m.Get("")
	if !ok {
		t.Fatal("node was expected")
	}
	if n.Value() != "foobar" {
		t.Fatalf("value does not match: %v", n.Value())
	}
}

func TestMapSet_NilValue(t *testing.T) {
	nm := testNodeManager[string, *struct{}]()
	m := New(nm)
	m.Compute("foo", func(n node.Node[string, *struct{}]) node.Node[string, *struct{}] {
		return nm.Create("foo", nil, 0, 1)
	})
	n, ok := m.Get("foo")
	if !ok {
		t.Fatal("nil node was expected")
	}
	if n.Value() != nil {
		t.Fatalf("value was not nil: %v", n.Value())
	}
}

func TestMapSet_NonNilValue(t *testing.T) {
	type foo struct{}
	nm := testNodeManager[string, *foo]()
	m := New[string, *foo](nm)
	newv := &foo{}
	newv2 := &foo{}
	got := m.Compute("foo", func(n node.Node[string, *foo]) node.Node[string, *foo] {
		return nm.Create("foo", newv2, 0, 1)
	})
	if got == nil {
		t.Fatal("node was expected")
	}
	if got.Value() != newv {
		t.Fatalf("value does not match: %v", newv)
	}
}

func TestMapRange(t *testing.T) {
	const numNodes = 1000
	nm := testNodeManager[string, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := strconv.Itoa(i)
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			return nm.Create(key, i, 0, 1)
		})
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

func TestMapRange_FalseReturned(t *testing.T) {
	nm := testNodeManager[string, int]()
	m := New(nm)
	for i := 0; i < 100; i++ {
		key := strconv.Itoa(i)
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			return nm.Create(key, i, 0, 1)
		})
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

func TestMapRange_NestedDelete(t *testing.T) {
	const numNodes = 256
	nm := testNodeManager[string, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := strconv.Itoa(i)
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			return nm.Create(key, i, 0, 1)
		})
	}
	m.Range(func(n1 node.Node[string, int]) bool {
		m.Compute(n1.Key(), func(n node.Node[string, int]) node.Node[string, int] {
			return nil
		})
		return true
	})
	for i := 0; i < numNodes; i++ {
		if _, ok := m.Get(strconv.Itoa(i)); ok {
			t.Fatalf("node found for %d", i)
		}
	}
}

func TestMapStringSet(t *testing.T) {
	const numNodes = 128
	nm := testNodeManager[string, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := strconv.Itoa(i)
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			return nm.Create(key, i, 0, 1)
		})
	}
	for i := 0; i < numNodes; i++ {
		n, ok := m.Get(strconv.Itoa(i))
		if !ok {
			t.Fatalf("node not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n)
		}
	}
}

func TestMapIntSet(t *testing.T) {
	const numNodes = 128
	nm := testNodeManager[int, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return nm.Create(i, i, 0, 1)
		})
	}
	for i := 0; i < numNodes; i++ {
		n, ok := m.Get(i)
		if !ok {
			t.Fatalf("node not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n)
		}
	}
}

func TestMapSet_StructKeys_IntValues(t *testing.T) {
	const numNodes = 128
	nm := testNodeManager[point, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := point{int32(i), -int32(i)}
		m.Compute(key, func(n node.Node[point, int]) node.Node[point, int] {
			return nm.Create(key, i, 0, 1)
		})
	}
	for i := 0; i < numNodes; i++ {
		n, ok := m.Get(point{int32(i), -int32(i)})
		if !ok {
			t.Fatalf("node not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n)
		}
	}
}

func TestMapSet_StructKeys_StructValues(t *testing.T) {
	const numNodes = 128
	nm := testNodeManager[point, point]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := point{int32(i), -int32(i)}
		value := point{-int32(i), int32(i)}
		m.Compute(key, func(n node.Node[point, point]) node.Node[point, point] {
			return nm.Create(key, value, 0, 1)
		})
	}
	for i := 0; i < numNodes; i++ {
		n, ok := m.Get(point{int32(i), -int32(i)})
		if !ok {
			t.Fatalf("node not found for %d", i)
		}
		v := n.Value()
		if v.x != -int32(i) {
			t.Fatalf("x value does not match for %d: %v", i, v)
		}
		if v.y != int32(i) {
			t.Fatalf("y value does not match for %d: %v", i, v)
		}
	}
}

// this code may break if the maphash.Hasher[k] structure changes.
type hasher struct {
	hash func(pointer unsafe.Pointer, seed uintptr) uintptr
	seed uintptr
}

func TestMapSet_WithCollisions(t *testing.T) {
	const numNodes = 1000
	nm := testNodeManager[int, int]()
	m := NewWithSize(nm, numNodes)
	table := (*mapTable[int, int])(atomic.LoadPointer(&m.table))
	hasher := (*hasher)((unsafe.Pointer)(&table.hasher))
	hasher.hash = func(ptr unsafe.Pointer, seed uintptr) uintptr {
		// We intentionally use an awful hash function here to make sure
		// that the map copes with key collisions.
		return 42
	}
	for i := 0; i < numNodes; i++ {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return nm.Create(i, i, 0, 1)
		})
	}
	for i := 0; i < numNodes; i++ {
		n, ok := m.Get(i)
		if !ok {
			t.Fatalf("node not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n)
		}
	}
}

func TestMapCompute_FunctionCalledOnce(t *testing.T) {
	nm := testNodeManager[int, int]()
	m := New(nm)
	for i := 0; i < 100; {
		key := i
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			var v int
			v, i = i, i+1
			return nm.Create(key, v, 0, 1)
		})
	}
	m.Range(func(n node.Node[int, int]) bool {
		if n.Key() != n.Value() {
			t.Fatalf("%dth key is not equal to value %d", n.Key(), n.Value())
		}
		return true
	})
}

func TestMapCompute(t *testing.T) {
	nm := testNodeManager[string, int]()
	m := New(nm)
	// Store a new value.
	n := m.Compute("foobar", func(n node.Node[string, int]) node.Node[string, int] {
		if n != nil {
			t.Fatalf("n should be nil when computing a new node: %v", n)
		}
		return nm.Create("foobar", 42, 0, 1)
	})
	if n == nil {
		t.Fatal("n should be non nil when computing a new value")
	}
	if n.Value() != 42 {
		t.Fatalf("n.Value() should be 42 when computing a new value: %d", n.Value())
	}
	// Update an existing value.
	n = m.Compute("foobar", func(n node.Node[string, int]) node.Node[string, int] {
		if n.Value() != 42 {
			t.Fatalf("n.Value() should be 42 when updating the value: %d", n.Value())
		}
		return nm.Create("foobar", n.Value()+42, 0, 1)
	})
	if n == nil {
		t.Fatal("n should be non nil when computing a new value")
	}
	if n.Value() != 84 {
		t.Fatalf("n.Value() should be 84 when computing a new value: %d", n.Value())
	}
	// Delete an existing value.
	n = m.Compute("foobar", func(n node.Node[string, int]) node.Node[string, int] {
		if n.Value() != 84 {
			t.Fatalf("n.Value() should be 84 when deleting the value: %d", n.Value())
		}
		return nil
	})
	if n != nil {
		t.Fatal("n should be nil when deleting the value")
	}
	// Try to delete a non-existing value. Notice different key.
	n = m.Compute("barbaz", func(n node.Node[string, int]) node.Node[string, int] {
		if n != nil {
			t.Fatalf("n should be nil when trying to delete a non-existing value")
		}

		return nil
	})
	if n != nil {
		t.Fatal("n should be nil when deleting the value")
	}
}

func TestMapStringSetThenDelete(t *testing.T) {
	const numNodes = 1000
	nm := testNodeManager[string, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := strconv.Itoa(i)
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			return nm.Create(key, i, 0, 1)
		})
	}
	for i := 0; i < numNodes; i++ {
		m.Compute(strconv.Itoa(i), func(n node.Node[string, int]) node.Node[string, int] {
			return nil
		})
		if _, ok := m.Get(strconv.Itoa(i)); ok {
			t.Fatalf("node was not expected for %d", i)
		}
	}
}

func TestMapIntSetThenDelete(t *testing.T) {
	const numNodes = 1000
	nm := testNodeManager[int32, int32]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		m.Compute(int32(i), func(n node.Node[int32, int32]) node.Node[int32, int32] {
			return nm.Create(int32(i), int32(i), 0, 1)
		})
	}
	for i := 0; i < numNodes; i++ {
		m.Compute(int32(i), func(n node.Node[int32, int32]) node.Node[int32, int32] {
			return nil
		})
		if _, ok := m.Get(int32(i)); ok {
			t.Fatalf("node was not expected for %d", i)
		}
	}
}

func TestMapStructSetThenDelete(t *testing.T) {
	const numNodes = 1000
	nm := testNodeManager[point, string]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := point{int32(i), 42}
		m.Compute(key, func(n node.Node[point, string]) node.Node[point, string] {
			return nm.Create(key, strconv.Itoa(i), 0, 1)
		})
	}
	for i := 0; i < numNodes; i++ {
		key := point{int32(i), 42}
		m.Compute(key, func(n node.Node[point, string]) node.Node[point, string] {
			return nil
		})
		if _, ok := m.Get(key); ok {
			t.Fatalf("node was not expected for %d", i)
		}
	}
}

func TestMapSetThenParallelDelete_DoesNotShrinkBelowMinTableLen(t *testing.T) {
	const numNodes = 1000
	nm := testNodeManager[int, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return nm.Create(i, i, 0, 1)
		})
	}

	cdone := make(chan bool)
	go func() {
		for i := 0; i < numNodes; i++ {
			m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
				return nil
			})
		}
		cdone <- true
	}()
	go func() {
		for i := 0; i < numNodes; i++ {
			m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
				return nil
			})
		}
		cdone <- true
	}()

	// Wait for the goroutines to finish.
	<-cdone
	<-cdone

	table := (*mapTable[int, int])(atomic.LoadPointer(&m.table))
	if len(table.buckets) != defaultMinMapTableLen {
		t.Fatalf("table length was different from the minimum: %d", len(table.buckets))
	}
}

func sizeBasedOnTypedRange(m *Map[string, int]) int {
	size := 0
	m.Range(func(n node.Node[string, int]) bool {
		size++
		return true
	})
	return size
}

func TestMapSize(t *testing.T) {
	const numNodes = 1000
	nm := testNodeManager[string, int]()
	m := New(nm)
	size := m.Size()
	if size != 0 {
		t.Fatalf("zero size expected: %d", size)
	}
	expectedSize := 0
	for i := 0; i < numNodes; i++ {
		key := strconv.Itoa(i)
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			return nm.Create(key, i, 0, 1)
		})
		expectedSize++
		size := m.Size()
		if size != expectedSize {
			t.Fatalf("size of %d was expected, got: %d", expectedSize, size)
		}
		rsize := sizeBasedOnTypedRange(m)
		if size != rsize {
			t.Fatalf("size does not match number of entries in Range: %v, %v", size, rsize)
		}
	}
	for i := 0; i < numNodes; i++ {
		m.Compute(strconv.Itoa(i), func(n node.Node[string, int]) node.Node[string, int] {
			return nil
		})
		expectedSize--
		size := m.Size()
		if size != expectedSize {
			t.Fatalf("size of %d was expected, got: %d", expectedSize, size)
		}
		rsize := sizeBasedOnTypedRange(m)
		if size != rsize {
			t.Fatalf("size does not match number of entries in Range: %v, %v", size, rsize)
		}
	}
}

func TestMapClear(t *testing.T) {
	const numNodes = 1000
	nm := testNodeManager[string, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := strconv.Itoa(i)
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			return nm.Create(key, i, 0, 1)
		})
	}
	size := m.Size()
	if size != numNodes {
		t.Fatalf("size of %d was expected, got: %d", numNodes, size)
	}
	m.Clear()
	size = m.Size()
	if size != 0 {
		t.Fatalf("zero size was expected, got: %d", size)
	}
	rsize := sizeBasedOnTypedRange(m)
	if rsize != 0 {
		t.Fatalf("zero number of entries in Range was expected, got: %d", rsize)
	}
}

func parallelRandTypedResizer(m *Map[string, int], numIters, numNodes int, cdone chan bool) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < numIters; i++ {
		coin := r.Int63n(2)
		for j := 0; j < numNodes; j++ {
			key := strconv.Itoa(j)
			if coin == 1 {
				m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
					return m.nodeManager.Create(key, j, 0, 1)
				})
			} else {
				m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
					return nil
				})
			}
		}
	}
	cdone <- true
}

func TestMapParallelResize(t *testing.T) {
	const numIters = 1_000
	const numNodes = 2 * nodesPerMapBucket * defaultMinMapTableLen
	nm := testNodeManager[string, int]()
	m := New(nm)
	cdone := make(chan bool)
	go parallelRandTypedResizer(m, numIters, numNodes, cdone)
	go parallelRandTypedResizer(m, numIters, numNodes, cdone)
	// Wait for the goroutines to finish.
	<-cdone
	<-cdone
	// Verify map contents.
	for i := 0; i < numNodes; i++ {
		n, ok := m.Get(strconv.Itoa(i))
		if !ok {
			// The entry may be deleted and that's ok.
			continue
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n)
		}
	}
	s := m.Size()
	if s > numNodes {
		t.Fatalf("unexpected size: %v", s)
	}
	rs := sizeBasedOnTypedRange(m)
	if s != rs {
		t.Fatalf("size does not match number of entries in Range: %v, %v", s, rs)
	}
}

func parallelRandTypedClearer(m *Map[string, int], numIters, numNodes int, cdone chan bool) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < numIters; i++ {
		coin := r.Int63n(2)
		for j := 0; j < numNodes; j++ {
			key := strconv.Itoa(j)
			if coin == 1 {
				m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
					return m.nodeManager.Create(key, j, 0, 1)
				})
			} else {
				m.Clear()
			}
		}
	}
	cdone <- true
}

func TestMapParallelClear(t *testing.T) {
	const numIters = 100
	const numNodes = 1_000
	nm := testNodeManager[string, int]()
	m := New(nm)
	cdone := make(chan bool)
	go parallelRandTypedClearer(m, numIters, numNodes, cdone)
	go parallelRandTypedClearer(m, numIters, numNodes, cdone)
	// Wait for the goroutines to finish.
	<-cdone
	<-cdone
	// Verify map size.
	s := m.Size()
	if s > numNodes {
		t.Fatalf("unexpected size: %v", s)
	}
	rs := sizeBasedOnTypedRange(m)
	if s != rs {
		t.Fatalf("size does not match number of entries in Range: %v, %v", s, rs)
	}
}

func parallelSeqTypedSetter(t *testing.T, m *Map[string, int], storeEach, numIters, numNodes int, cdone chan bool) {
	for i := 0; i < numIters; i++ {
		for j := 0; j < numNodes; j++ {
			//nolint:gocritic // nesting is normal here
			if storeEach == 0 || j%storeEach == 0 {
				key := strconv.Itoa(j)
				m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
					return m.nodeManager.Create(key, j, 0, 1)
				})
				// Due to atomic snapshots we must see a "<j>"/j pair.
				n, ok := m.Get(key)
				if !ok {
					t.Errorf("node was not found for %d", j)
					break
				}
				if n.Value() != j {
					t.Errorf("value was not expected for %d: %v", j, n)
					break
				}
			}
		}
	}
	cdone <- true
}

func TestMapParallelSetter(t *testing.T) {
	const numSetters = 4
	const numIters = 10_000
	const numNodes = 100
	nm := testNodeManager[string, int]()
	m := New(nm)
	cdone := make(chan bool)
	for i := 0; i < numSetters; i++ {
		go parallelSeqTypedSetter(t, m, i, numIters, numNodes, cdone)
	}
	// Wait for the goroutines to finish.
	for i := 0; i < numSetters; i++ {
		<-cdone
	}
	// Verify map contents.
	for i := 0; i < numNodes; i++ {
		n, ok := m.Get(strconv.Itoa(i))
		if !ok {
			t.Fatalf("node not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n)
		}
	}
}

func parallelRandTypedSetter(t *testing.T, m *Map[string, int], numIters, numNodes int, cdone chan bool) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < numIters; i++ {
		j := r.Intn(numNodes)
		key := strconv.Itoa(j)
		newNode := m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			if n != nil {
				return n
			}
			return m.nodeManager.Create(key, j, 0, 1)
		})
		if newNode != nil {
			if newNode.Value() != j {
				t.Errorf("value was not expected for %d: %d", j, newNode.Value())
			}
		}
	}
	cdone <- true
}

func parallelRandTypedDeleter(t *testing.T, m *Map[string, int], numIters, numNodes int, cdone chan bool) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < numIters; i++ {
		j := r.Intn(numNodes)
		key := strconv.Itoa(j)
		var oldNode node.Node[string, int]
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			oldNode = n
			return nil
		})
		if oldNode != nil {
			if oldNode.Value() != j {
				t.Errorf("value was not expected for %d: %d", j, oldNode.Value())
			}
		}
	}
	cdone <- true
}

func parallelTypedGetter(t *testing.T, m *Map[string, int], numIters, numNodes int, cdone chan bool) {
	for i := 0; i < numIters; i++ {
		for j := 0; j < numNodes; j++ {
			// Due to atomic snapshots we must either see no entry, or a "<j>"/j pair.
			if n, ok := m.Get(strconv.Itoa(j)); ok {
				if n.Value() != j {
					t.Errorf("value was not expected for %d: %d", j, n.Value())
				}
			}
		}
	}
	cdone <- true
}

func TestMapAtomicSnapshot(t *testing.T) {
	const numIters = 100_000
	const numNodes = 100
	nm := testNodeManager[string, int]()
	m := New(nm)
	cdone := make(chan bool)
	// Update or delete random entry in parallel with gets.
	go parallelRandTypedSetter(t, m, numIters, numNodes, cdone)
	go parallelRandTypedDeleter(t, m, numIters, numNodes, cdone)
	go parallelTypedGetter(t, m, numIters, numNodes, cdone)
	// Wait for the goroutines to finish.
	for i := 0; i < 3; i++ {
		<-cdone
	}
}

func TestMapParallelSetsAndDeletes(t *testing.T) {
	const numWorkers = 2
	const numIters = 100_000
	const numNodes = 1000
	nm := testNodeManager[string, int]()
	m := New(nm)
	cdone := make(chan bool)
	// Update random entry in parallel with deletes.
	for i := 0; i < numWorkers; i++ {
		go parallelRandTypedSetter(t, m, numIters, numNodes, cdone)
		go parallelRandTypedDeleter(t, m, numIters, numNodes, cdone)
	}
	// Wait for the goroutines to finish.
	for i := 0; i < 2*numWorkers; i++ {
		<-cdone
	}
}

func parallelTypedComputer(m *Map[uint64, uint64], numIters, numNodes int, cdone chan bool) {
	for i := 0; i < numIters; i++ {
		for j := 0; j < numNodes; j++ {
			m.Compute(uint64(j), func(oldNode node.Node[uint64, uint64]) node.Node[uint64, uint64] {
				v := uint64(1)
				if oldNode != nil {
					v = oldNode.Value() + 1
				}
				return m.nodeManager.Create(uint64(j), v, 0, 1)
			})
		}
	}
	cdone <- true
}

func TestMapParallelComputes(t *testing.T) {
	const numWorkers = 4 // Also stands for numNodes.
	const numIters = 10_000
	nm := testNodeManager[uint64, uint64]()
	m := New(nm)
	cdone := make(chan bool)
	for i := 0; i < numWorkers; i++ {
		go parallelTypedComputer(m, numIters, numWorkers, cdone)
	}
	// Wait for the goroutines to finish.
	for i := 0; i < numWorkers; i++ {
		<-cdone
	}
	// Verify map contents.
	for i := 0; i < numWorkers; i++ {
		n, ok := m.Get(uint64(i))
		if !ok {
			t.Fatalf("node not found for %d", i)
		}
		if n.Value() != numWorkers*numIters {
			t.Fatalf("values do not match for %d: %v", i, n.Value())
		}
	}
}

func parallelTypedRangeSetter(m *Map[int, int], numNodes int, stopFlag *int64, cdone chan bool) {
	for {
		for i := 0; i < numNodes; i++ {
			m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
				return m.nodeManager.Create(i, i, 0, 1)
			})
		}
		if atomic.LoadInt64(stopFlag) != 0 {
			break
		}
	}
	cdone <- true
}

func parallelTypedRangeDeleter(m *Map[int, int], numSetter int, stopFlag *int64, cdone chan bool) {
	for {
		for i := 0; i < numSetter; i++ {
			m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
				return nil
			})
		}
		if atomic.LoadInt64(stopFlag) != 0 {
			break
		}
	}
	cdone <- true
}

func TestMapParallelRange(t *testing.T) {
	const numNodes = 10_000
	nm := testNodeManager[int, int]()
	m := NewWithSize(nm, numNodes)
	for i := 0; i < numNodes; i++ {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return nm.Create(i, i, 0, 1)
		})
	}
	// Start goroutines that would be storing and deleting items in parallel.
	cdone := make(chan bool)
	stopFlag := int64(0)
	go parallelTypedRangeSetter(m, numNodes, &stopFlag, cdone)
	go parallelTypedRangeDeleter(m, numNodes, &stopFlag, cdone)
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
		t.Fatal("no entries were met when iterating")
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

func parallelTypedShrinker(
	t *testing.T,
	m *Map[uint64, *point],
	numIters, numNodes int,
	stopFlag *int64, cdone chan bool,
) {
	for i := 0; i < numIters; i++ {
		for j := 0; j < numNodes; j++ {
			wasPresent := false
			n := m.Compute(uint64(j), func(n node.Node[uint64, *point]) node.Node[uint64, *point] {
				if n != nil {
					wasPresent = true
					return n
				}
				return m.nodeManager.Create(uint64(j), &point{int32(j), int32(j)}, 0, 1)
			})
			if wasPresent {
				t.Errorf("node was present for %d: %v", j, n.Value())
			}
		}
		for j := 0; j < numNodes; j++ {
			m.Compute(uint64(j), func(n node.Node[uint64, *point]) node.Node[uint64, *point] {
				return nil
			})
		}
	}
	atomic.StoreInt64(stopFlag, 1)
	cdone <- true
}

func parallelTypedUpdater(t *testing.T, m *Map[uint64, *point], idx int, stopFlag *int64, cdone chan bool) {
	for atomic.LoadInt64(stopFlag) != 1 {
		sleepUs := int(xruntime.Fastrand() % 10)
		wasPresent := false
		n := m.Compute(uint64(idx), func(n node.Node[uint64, *point]) node.Node[uint64, *point] {
			if n != nil {
				wasPresent = true
				return n
			}
			return m.nodeManager.Create(uint64(idx), &point{int32(idx), int32(idx)}, 0, 1)
		})
		if wasPresent {
			t.Errorf("value was present for %d: %v", idx, n.Value())
		}
		time.Sleep(time.Duration(sleepUs) * time.Microsecond)
		if _, ok := m.Get(uint64(idx)); !ok {
			t.Errorf("node was not found for %d", idx)
		}
		m.Compute(uint64(idx), func(n node.Node[uint64, *point]) node.Node[uint64, *point] {
			return nil
		})
	}
	cdone <- true
}

func TestMapDoesNotLoseNodesOnResize(t *testing.T) {
	const numIters = 10_000
	const numNodes = 128
	nm := testNodeManager[uint64, *point]()
	m := New(nm)
	cdone := make(chan bool)
	stopFlag := int64(0)
	go parallelTypedShrinker(t, m, numIters, numNodes, &stopFlag, cdone)
	go parallelTypedUpdater(t, m, numNodes, &stopFlag, cdone)
	// Wait for the goroutines to finish.
	<-cdone
	<-cdone
	// Verify map contents.
	if s := m.Size(); s != 0 {
		t.Fatalf("map is not empty: %d", s)
	}
}
