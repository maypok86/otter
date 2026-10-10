// Copyright (c) 2023 Alexey Mayshev and contributors. All rights reserved.
// Copyright (c) 2021 Andrey Pechkurov. All rights reserved.
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
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

func newTestNode[K comparable, V any](nm mapNodeManager[K, V, node.Node[K, V]], key K, value V) node.Node[K, V] {
	m, ok := nm.(*node.Manager[K, V])
	if !ok {
		panic("not valid node manager")
	}
	return m.Create(key, value, 0, 0, 1)
}

func testNodeManager[K comparable, V any]() mapNodeManager[K, V, node.Node[K, V]] {
	return node.NewManager[K, V](node.Config{})
}

type point struct {
	x int32
	y int32
}

func TestMap_BucketStructSize(t *testing.T) {
	t.Parallel()

	size := unsafe.Sizeof(bucketPadded{})
	if size != 64 {
		t.Fatalf("size of 64B (one cache line) is expected, got: %d", size)
	}
}

func TestMap_BucketLockIsNotAtTail(t *testing.T) {
	t.Parallel()

	// The tail of a bucket can share a cache line with the next bucket,
	// so it must hold next, which is rarely written, rather than mu.
	var b bucket
	if unsafe.Offsetof(b.next)+unsafe.Sizeof(b.next) != unsafe.Sizeof(b) {
		t.Fatalf("next is expected to be the last field of a bucket")
	}
}

func TestMap_MissingNode(t *testing.T) {
	t.Parallel()

	nm := testNodeManager[string, string]()
	m := New(nm)
	n := m.Get("foo")
	if n != nil {
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
	t.Parallel()

	nm := testNodeManager[string, string]()
	m := New(nm)
	m.Compute("", func(n node.Node[string, string]) node.Node[string, string] {
		return newTestNode(nm, "", "foobar")
	})
	n := m.Get("")
	if n == nil {
		t.Fatal("node was expected")
	}
	if n.Value() != "foobar" {
		t.Fatalf("value does not match: %v", n.Value())
	}
}

func TestMapSet_NilValue(t *testing.T) {
	t.Parallel()

	nm := testNodeManager[string, *struct{}]()
	m := New(nm)
	m.Compute("foo", func(n node.Node[string, *struct{}]) node.Node[string, *struct{}] {
		return newTestNode(nm, "foo", nil)
	})
	n := m.Get("foo")
	if n == nil {
		t.Fatal("nil node was expected")
	}
	if n.Value() != nil {
		t.Fatalf("value was not nil: %v", n.Value())
	}
}

func TestMapSet_NonNilValue(t *testing.T) {
	t.Parallel()

	type foo struct{}
	nm := testNodeManager[string, *foo]()
	m := New[string, *foo](nm)
	newv := &foo{}
	newv2 := &foo{}
	got := m.Compute("foo", func(n node.Node[string, *foo]) node.Node[string, *foo] {
		return newTestNode(nm, "foo", newv2)
	})
	if got == nil {
		t.Fatal("node was expected")
	}
	if got.Value() != newv {
		t.Fatalf("value does not match: %v", newv)
	}
}

func TestMapRange(t *testing.T) {
	t.Parallel()

	const numNodes = 1000
	nm := testNodeManager[string, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := strconv.Itoa(i)
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			return newTestNode(nm, key, i)
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
	t.Parallel()

	nm := testNodeManager[string, int]()
	m := New(nm)
	for i := 0; i < 100; i++ {
		key := strconv.Itoa(i)
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			return newTestNode(nm, key, i)
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
	t.Parallel()

	const numNodes = 256
	nm := testNodeManager[string, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := strconv.Itoa(i)
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			return newTestNode(nm, key, i)
		})
	}
	m.Range(func(n1 node.Node[string, int]) bool {
		m.Compute(n1.Key(), func(n node.Node[string, int]) node.Node[string, int] {
			return nil
		})
		return true
	})
	for i := 0; i < numNodes; i++ {
		if n := m.Get(strconv.Itoa(i)); n != nil {
			t.Fatalf("node found for %d", i)
		}
	}
}

func TestMapStringSet(t *testing.T) {
	t.Parallel()

	const numNodes = 128
	nm := testNodeManager[string, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := strconv.Itoa(i)
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			return newTestNode(nm, key, i)
		})
	}
	for i := 0; i < numNodes; i++ {
		n := m.Get(strconv.Itoa(i))
		if n == nil {
			t.Fatalf("node not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n)
		}
	}
}

func TestMapIntSet(t *testing.T) {
	t.Parallel()

	const numNodes = 128
	nm := testNodeManager[int, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return newTestNode(nm, i, i)
		})
	}
	for i := 0; i < numNodes; i++ {
		n := m.Get(i)
		if n == nil {
			t.Fatalf("node not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n)
		}
	}
}

func TestMapSet_StructKeys_IntValues(t *testing.T) {
	t.Parallel()

	const numNodes = 128
	nm := testNodeManager[point, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := point{int32(i), -int32(i)}
		m.Compute(key, func(n node.Node[point, int]) node.Node[point, int] {
			return newTestNode(nm, key, i)
		})
	}
	for i := 0; i < numNodes; i++ {
		n := m.Get(point{int32(i), -int32(i)})
		if n == nil {
			t.Fatalf("node not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n)
		}
	}
}

func TestMapSet_StructKeys_StructValues(t *testing.T) {
	t.Parallel()

	const numNodes = 128
	nm := testNodeManager[point, point]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := point{int32(i), -int32(i)}
		value := point{-int32(i), int32(i)}
		m.Compute(key, func(n node.Node[point, point]) node.Node[point, point] {
			return newTestNode(nm, key, value)
		})
	}
	for i := 0; i < numNodes; i++ {
		n := m.Get(point{int32(i), -int32(i)})
		if n == nil {
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

func TestMapCompute_FunctionCalledOnce(t *testing.T) {
	t.Parallel()

	nm := testNodeManager[int, int]()
	m := New(nm)
	for i := 0; i < 100; {
		key := i
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			var v int
			v, i = i, i+1
			return newTestNode(nm, key, v)
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
	t.Parallel()

	nm := testNodeManager[string, int]()
	m := New(nm)
	// Store a new value.
	n := m.Compute("foobar", func(n node.Node[string, int]) node.Node[string, int] {
		if n != nil {
			t.Fatalf("n should be nil when computing a new node: %v", n)
		}
		return newTestNode(nm, "foobar", 42)
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
		return newTestNode(nm, "foobar", n.Value()+42)
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
	t.Parallel()

	const numNodes = 1000
	nm := testNodeManager[string, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := strconv.Itoa(i)
		m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			return newTestNode(nm, key, i)
		})
	}
	for i := 0; i < numNodes; i++ {
		m.Compute(strconv.Itoa(i), func(n node.Node[string, int]) node.Node[string, int] {
			return nil
		})
		if n := m.Get(strconv.Itoa(i)); n != nil {
			t.Fatalf("node was not expected for %d", i)
		}
	}
}

func TestMapIntSetThenDelete(t *testing.T) {
	t.Parallel()

	const numNodes = 1000
	nm := testNodeManager[int32, int32]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		m.Compute(int32(i), func(n node.Node[int32, int32]) node.Node[int32, int32] {
			return newTestNode(nm, int32(i), int32(i))
		})
	}
	for i := 0; i < numNodes; i++ {
		m.Compute(int32(i), func(n node.Node[int32, int32]) node.Node[int32, int32] {
			return nil
		})
		if n := m.Get(int32(i)); n != nil {
			t.Fatalf("node was not expected for %d", i)
		}
	}
}

func TestMapStructSetThenDelete(t *testing.T) {
	t.Parallel()

	const numNodes = 1000
	nm := testNodeManager[point, string]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		key := point{int32(i), 42}
		m.Compute(key, func(n node.Node[point, string]) node.Node[point, string] {
			return newTestNode(nm, key, strconv.Itoa(i))
		})
	}
	for i := 0; i < numNodes; i++ {
		key := point{int32(i), 42}
		m.Compute(key, func(n node.Node[point, string]) node.Node[point, string] {
			return nil
		})
		if n := m.Get(key); n != nil {
			t.Fatalf("node was not expected for %d", i)
		}
	}
}

func TestMap_MetaMatchesNodes(t *testing.T) {
	t.Parallel()

	const numNodes = 1000
	nm := testNodeManager[int, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return newTestNode(nm, i, i)
		})
	}
	for i := 0; i < numNodes; i += 2 {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return nil
		})
	}

	// A slot is occupied exactly when its meta byte has the high bit set,
	// and the bytes past the last slot stay zero, so that a zeroed bucket
	// is an empty one.
	table := m.table.Load()
	for i := range table.buckets {
		for b := &table.buckets[i]; b != nil; b = b.next.Load() {
			metaw := b.meta.Load()
			if metaw&^metaMask != 0 {
				t.Fatalf("bytes past the last slot are not zero: %#x", metaw)
			}
			for j := 0; j < nodesPerMapBucket; j++ {
				occupied := metaw>>(j*8)&0x80 != 0
				if occupied != (b.nodes[j] != nil) {
					t.Fatalf("meta %#x does not match slot %d (node: %v)", metaw, j, b.nodes[j])
				}
			}
		}
	}
}

// keysInRootBucket returns n int keys that m puts into its first bucket.
func keysInRootBucket(m *Map[int, int, node.Node[int, int]], n int) []int {
	table := m.table.Load()
	//nolint:gosec // there is no overflow
	mask := uint64(len(table.buckets) - 1)
	keys := make([]int, 0, n)
	for k := 0; len(keys) < n; k++ {
		if h1(table.hasher.Hash(k))&mask == 0 {
			keys = append(keys, k)
		}
	}
	return keys
}

func chainLen(b *bucketPadded) int {
	l := 0
	for ; b != nil; b = b.next.Load() {
		l++
	}
	return l
}

func TestMap_UnlinksEmptyOverflowBucket(t *testing.T) {
	t.Parallel()

	nm := testNodeManager[int, int]()
	m := New(nm)
	set := func(k int) {
		m.Compute(k, func(n node.Node[int, int]) node.Node[int, int] {
			return newTestNode(nm, k, k)
		})
	}
	del := func(k int) {
		m.Compute(k, func(n node.Node[int, int]) node.Node[int, int] {
			return nil
		})
	}
	// Fill the root bucket, one overflow bucket and part of a second one.
	keys := keysInRootBucket(m, 2*nodesPerMapBucket+3)
	root := keys[:nodesPerMapBucket:nodesPerMapBucket]
	middle := keys[nodesPerMapBucket : 2*nodesPerMapBucket : 2*nodesPerMapBucket]
	tail := keys[2*nodesPerMapBucket:]
	for _, k := range keys {
		set(k)
	}
	rootb := &m.table.Load().buckets[0]
	if l := chainLen(rootb); l != 3 {
		t.Fatalf("a chain of 3 buckets was expected, got: %d", l)
	}

	// Without unlinking, a table that never resizes keeps the longest chain
	// it ever had, and every miss walks all of it.
	for _, k := range middle {
		del(k)
	}
	if l := chainLen(rootb); l != 2 {
		t.Fatalf("the empty overflow bucket was expected to be unlinked, chain length: %d", l)
	}
	for _, k := range append(append([]int{}, root...), tail...) {
		if m.Get(k) == nil {
			t.Fatalf("node was not found for %d after unlinking", k)
		}
	}

	// Readers must keep finding the nodes behind a bucket that is unlinked
	// under them. Each round links a bucket of middle keys after the root
	// and a bucket of tail keys after it, then unlinks the middle bucket
	// while readers look up the tail keys. A miss only counts while the
	// round is in that phase, before the tail keys are deleted too.
	for _, k := range tail {
		del(k)
	}
	var (
		wg      sync.WaitGroup
		phase   atomic.Int64
		stop    atomic.Bool
		missing atomic.Int64
	)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				p := phase.Load()
				if p%2 == 0 {
					continue
				}
				for _, k := range tail {
					if m.Get(k) == nil && phase.Load() == p {
						missing.Add(1)
					}
				}
			}
		}()
	}
	for r := int64(0); r < 20_000; r++ {
		for _, k := range middle {
			set(k)
		}
		for _, k := range tail {
			set(k)
		}
		phase.Store(2*r + 1)
		for _, k := range middle {
			del(k)
		}
		phase.Store(2*r + 2)
		for _, k := range tail {
			del(k)
		}
	}
	stop.Store(true)
	wg.Wait()
	if n := missing.Load(); n != 0 {
		t.Fatalf("nodes behind an unlinked bucket were not found %d times", n)
	}
}

func TestMapSetThenParallelDelete_DoesNotShrinkBelowMinTableLen(t *testing.T) {
	t.Parallel()

	const numNodes = 1000
	nm := testNodeManager[int, int]()
	m := New(nm)
	for i := 0; i < numNodes; i++ {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return newTestNode(nm, i, i)
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

	table := m.table.Load()
	if len(table.buckets) != defaultMinMapTableLen {
		t.Fatalf("table length was different from the minimum: %d", len(table.buckets))
	}
}

func sizeBasedOnTypedRange(m *Map[string, int, node.Node[string, int]]) int {
	size := 0
	m.Range(func(n node.Node[string, int]) bool {
		size++
		return true
	})
	return size
}

func TestMapSize(t *testing.T) {
	t.Parallel()

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
			return newTestNode(nm, key, i)
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

func TestMap_ResizeKeepsHasher(t *testing.T) {
	t.Parallel()

	// copyBuckets writes to the new table without locks, which is only safe
	// while a node can't move to a bucket owned by another goroutine, i.e.
	// while growing and shrinking keep the hasher.
	nm := testNodeManager[int, int]()
	m := New(nm)
	hasher := m.table.Load().hasher
	const numNodes = 10 * defaultMinMapTableLen * nodesPerMapBucket
	for i := 0; i < numNodes; i++ {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return newTestNode(nm, i, i)
		})
	}
	if m.totalGrowths.Load() == 0 {
		t.Fatal("the table was expected to grow")
	}
	if m.table.Load().hasher != hasher {
		t.Fatal("growing the table changed the hasher")
	}
	for i := 0; i < numNodes; i++ {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return nil
		})
	}
	if m.totalShrinks.Load() == 0 {
		t.Fatal("the table was expected to shrink")
	}
	if m.table.Load().hasher != hasher {
		t.Fatal("shrinking the table changed the hasher")
	}
}

func TestMap_LateGrowDoesNotGrowAgain(t *testing.T) {
	t.Parallel()

	// A writer that saw a full chain unlocks its bucket and only then starts
	// the resize. If another writer has grown the table in between, the late
	// one must not grow the new table again.
	nm := testNodeManager[int, int]()
	m := New(nm)
	seen := m.table.Load()
	m.resize(seen, mapGrowHint)
	grown := len(m.table.Load().buckets)
	m.resize(seen, mapGrowHint)
	if l := len(m.table.Load().buckets); l != grown {
		t.Fatalf("a table of %d buckets was expected, got: %d", grown, l)
	}
	if g := m.totalGrowths.Load(); g != 1 {
		t.Fatalf("one growth was expected, got: %d", g)
	}
}

func TestMapParallelCopy(t *testing.T) {
	t.Parallel()

	// Tables of 2*minBucketsPerGoroutine buckets and more are copied by up to
	// GOMAXPROCS goroutines; grow well past that and shrink back while readers
	// check that no node goes missing.
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("a table is copied by one goroutine with GOMAXPROCS=1")
	}
	nm := testNodeManager[int, int]()
	m := New(nm)
	const numNodes = 64 * minBucketsPerGoroutine * nodesPerMapBucket
	var inserted, stop atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for stop.Load() == 0 {
			n := int(inserted.Load())
			for i := 0; i < n; i += 97 {
				if got := m.Get(i); got == nil || got.Value() != i {
					t.Errorf("node %d is missing", i)
					return
				}
			}
		}
	}()
	for i := 0; i < numNodes; i++ {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return newTestNode(nm, i, i)
		})
		inserted.Store(int64(i + 1))
	}
	stop.Store(1)
	<-done
	if got := len(m.table.Load().buckets); got < 2*minBucketsPerGoroutine {
		t.Fatalf("the table has %d buckets, too few for a parallel copy", got)
	}
	if size := m.Size(); size != numNodes {
		t.Fatalf("size is %d, want %d", size, numNodes)
	}
	for i := 0; i < numNodes; i++ {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return nil
		})
		if i%1000 == 0 {
			for j := i + 1; j < numNodes; j += 101 {
				if got := m.Get(j); got == nil || got.Value() != j {
					t.Fatalf("node %d is missing after deleting %d nodes", j, i+1)
				}
			}
		}
	}
	if got := len(m.table.Load().buckets); got != m.minTableLen {
		t.Fatalf("the table has %d buckets after deleting everything, want %d", got, m.minTableLen)
	}
	if size := m.Size(); size != 0 {
		t.Fatalf("size is %d after deleting everything", size)
	}
}

func TestMapParallelWritersDuringResize(t *testing.T) {
	t.Parallel()

	// Grow a table well past the parallel copy threshold from many writers at
	// once, so that writers keep running into resizes, then shrink it back, and
	// check that no node is lost or duplicated.
	nm := testNodeManager[int, int]()
	m := New(nm)
	const (
		numWriters = 16
		perWriter  = 16 * minBucketsPerGoroutine * nodesPerMapBucket
	)
	var wg sync.WaitGroup
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := base; i < base+perWriter; i++ {
				m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
					return newTestNode(nm, i, i)
				})
			}
		}(w * perWriter)
	}
	wg.Wait()
	if size := m.Size(); size != numWriters*perWriter {
		t.Fatalf("size is %d, want %d", size, numWriters*perWriter)
	}
	for i := 0; i < numWriters*perWriter; i++ {
		if n := m.Get(i); n == nil || n.Value() != i {
			t.Fatalf("node %d is missing", i)
		}
	}
	if got := sizeBasedOnTypedRangeInt(m); got != numWriters*perWriter {
		t.Fatalf("range visited %d nodes, want %d", got, numWriters*perWriter)
	}

	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := base; i < base+perWriter; i++ {
				m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
					return nil
				})
			}
		}(w * perWriter)
	}
	wg.Wait()
	if size := m.Size(); size != 0 {
		t.Fatalf("size is %d after deleting everything", size)
	}
	if got := sizeBasedOnTypedRangeInt(m); got != 0 {
		t.Fatalf("range visited %d nodes after deleting everything", got)
	}
}

func sizeBasedOnTypedRangeInt(m *Map[int, int, node.Node[int, int]]) int {
	size := 0
	m.Range(func(n node.Node[int, int]) bool {
		size++
		return true
	})
	return size
}

func parallelRandTypedResizer(m *Map[string, int, node.Node[string, int]], numIters, numNodes int, cdone chan bool) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < numIters; i++ {
		coin := r.Int63n(2)
		for j := 0; j < numNodes; j++ {
			key := strconv.Itoa(j)
			if coin == 1 {
				m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
					return newTestNode(m.nodeManager, key, j)
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
	t.Parallel()

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
		n := m.Get(strconv.Itoa(i))
		if n == nil {
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

func parallelSeqTypedSetter(
	t *testing.T,
	m *Map[string, int, node.Node[string, int]],
	storeEach, numIters, numNodes int,
	cdone chan bool,
) {
	for i := 0; i < numIters; i++ {
		for j := 0; j < numNodes; j++ {
			//nolint:gocritic // nesting is normal here
			if storeEach == 0 || j%storeEach == 0 {
				key := strconv.Itoa(j)
				m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
					return newTestNode(m.nodeManager, key, j)
				})
				// Due to atomic snapshots we must see a "<j>"/j pair.
				n := m.Get(key)
				if n == nil {
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
	t.Parallel()

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
		n := m.Get(strconv.Itoa(i))
		if n == nil {
			t.Fatalf("node not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n)
		}
	}
}

func parallelRandTypedSetter(
	t *testing.T,
	m *Map[string, int, node.Node[string, int]],
	numIters, numNodes int,
	cdone chan bool,
) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < numIters; i++ {
		j := r.Intn(numNodes)
		key := strconv.Itoa(j)
		newNode := m.Compute(key, func(n node.Node[string, int]) node.Node[string, int] {
			if n != nil {
				return n
			}
			return newTestNode(m.nodeManager, key, j)
		})
		if newNode != nil {
			if newNode.Value() != j {
				t.Errorf("value was not expected for %d: %d", j, newNode.Value())
			}
		}
	}
	cdone <- true
}

func parallelRandTypedDeleter(
	t *testing.T,
	m *Map[string, int, node.Node[string, int]],
	numIters, numNodes int,
	cdone chan bool,
) {
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

func parallelTypedGetter(
	t *testing.T,
	m *Map[string, int, node.Node[string, int]],
	numIters, numNodes int,
	cdone chan bool,
) {
	for i := 0; i < numIters; i++ {
		for j := 0; j < numNodes; j++ {
			// Due to atomic snapshots we must either see no entry, or a "<j>"/j pair.
			if n := m.Get(strconv.Itoa(j)); n != nil {
				if n.Value() != j {
					t.Errorf("value was not expected for %d: %d", j, n.Value())
				}
			}
		}
	}
	cdone <- true
}

func TestMapAtomicSnapshot(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

func parallelTypedComputer(m *Map[uint64, uint64, node.Node[uint64, uint64]], numIters, numNodes int, cdone chan bool) {
	for i := 0; i < numIters; i++ {
		for j := 0; j < numNodes; j++ {
			m.Compute(uint64(j), func(oldNode node.Node[uint64, uint64]) node.Node[uint64, uint64] {
				v := uint64(1)
				if oldNode != nil {
					v = oldNode.Value() + 1
				}
				return newTestNode(m.nodeManager, uint64(j), v)
			})
		}
	}
	cdone <- true
}

func TestMapParallelComputes(t *testing.T) {
	t.Parallel()

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
		n := m.Get(uint64(i))
		if n == nil {
			t.Fatalf("node not found for %d", i)
		}
		if n.Value() != numWorkers*numIters {
			t.Fatalf("values do not match for %d: %v", i, n.Value())
		}
	}
}

func parallelTypedRangeSetter(m *Map[int, int, node.Node[int, int]], numNodes int, stopFlag *int64, cdone chan bool) {
	for {
		for i := 0; i < numNodes; i++ {
			m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
				return newTestNode(m.nodeManager, i, i)
			})
		}
		if atomic.LoadInt64(stopFlag) != 0 {
			break
		}
	}
	cdone <- true
}

func parallelTypedRangeDeleter(m *Map[int, int, node.Node[int, int]], numSetter int, stopFlag *int64, cdone chan bool) {
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
	t.Parallel()

	const numNodes = 10_000
	nm := testNodeManager[int, int]()
	m := NewWithSize(nm, numNodes)
	for i := 0; i < numNodes; i++ {
		m.Compute(i, func(n node.Node[int, int]) node.Node[int, int] {
			return newTestNode(nm, i, i)
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
	m *Map[uint64, *point, node.Node[uint64, *point]],
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
				return newTestNode(m.nodeManager, uint64(j), &point{int32(j), int32(j)})
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

func parallelTypedUpdater(
	t *testing.T,
	m *Map[uint64, *point, node.Node[uint64, *point]],
	idx int, stopFlag *int64,
	cdone chan bool,
) {
	for atomic.LoadInt64(stopFlag) != 1 {
		sleepUs := int(xruntime.Fastrand() % 10)
		wasPresent := false
		n := m.Compute(uint64(idx), func(n node.Node[uint64, *point]) node.Node[uint64, *point] {
			if n != nil {
				wasPresent = true
				return n
			}
			return newTestNode(m.nodeManager, uint64(idx), &point{int32(idx), int32(idx)})
		})
		if wasPresent {
			t.Errorf("value was present for %d: %v", idx, n.Value())
		}
		time.Sleep(time.Duration(sleepUs) * time.Microsecond)
		if n := m.Get(uint64(idx)); n == nil {
			t.Errorf("node was not found for %d", idx)
		}
		m.Compute(uint64(idx), func(n node.Node[uint64, *point]) node.Node[uint64, *point] {
			return nil
		})
	}
	cdone <- true
}

func TestMapDoesNotLoseNodesOnResize(t *testing.T) {
	t.Parallel()

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
