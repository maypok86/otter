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
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

// testValueStorage checks that values of type V are kept in the given value storage, survive
// writes and reads unchanged, and that the first update is applied in place exactly when the
// value is kept in one atomic word; otherwise it replaces the node with a boxed one.
func testValueStorage[V any](t *testing.T, storage string, v1, v2 V) {
	t.Helper()

	c := Must(&Options[int, V]{MaximumSize: 10})
	require.Equal(t, storage, c.cache.nodeManager.ValueStorage())
	c.Set(1, v1)
	n := c.cache.hashmap.Get(1)
	require.Equal(t, storage != "", n.CanSetValue())
	got, _ := c.GetIfPresent(1)
	require.Equal(t, v1, got)

	c.Set(1, v2)
	got, _ = c.GetIfPresent(1)
	require.Equal(t, v2, got)
	inPlace := n.AsPointer() == c.cache.hashmap.Get(1).AsPointer()
	require.Equal(t, storage != "", inPlace, "the first update in place")
	require.True(t, c.cache.hashmap.Get(1).CanSetValue())

	c.Set(1, v1)
	got, _ = c.GetIfPresent(1)
	require.Equal(t, v1, got)
}

func TestCache_ValueStorage(t *testing.T) {
	t.Parallel()

	type word32 struct {
		a, b int16
	}
	type word64 struct {
		a, b int32
	}
	type withPointer struct {
		p *int
	}
	x, y := 1, 2

	t.Run("pointer", func(t *testing.T) {
		t.Parallel()
		testValueStorage(t, "p", &x, &y)
		testValueStorage(t, "p", map[int]int{1: 1}, map[int]int{2: 2})
		testValueStorage(t, "p", make(chan int), make(chan int))
		testValueStorage(t, "p", unsafe.Pointer(&x), unsafe.Pointer(&y))

		f1 := func() int { return x }
		f2 := func() int { return y }
		c := Must(&Options[int, func() int]{MaximumSize: 10})
		c.Set(1, f1)
		c.Set(1, f2)
		got, _ := c.GetIfPresent(1)
		require.Equal(t, 2, got())
	})
	t.Run("word", func(t *testing.T) {
		t.Parallel()
		testValueStorage(t, "u64", true, false)
		testValueStorage(t, "u64", int8(-1), int8(7))
		testValueStorage(t, "u64", int32(-5), int32(1<<30))
		testValueStorage(t, "u64", float32(-1.5), float32(3.25))
		testValueStorage(t, "u64", [3]byte{1, 2, 3}, [3]byte{4, 5, 6})
		testValueStorage(t, "u64", word32{a: -1, b: 2}, word32{a: 3, b: -4})
		testValueStorage(t, "u64", int64(-1), int64(1)<<62)
		testValueStorage(t, "u64", uint64(1)<<63, uint64(5))
		testValueStorage(t, "u64", -1.5, 1e300)
		testValueStorage(t, "u64", complex64(1+2i), complex64(-3-4i))
		testValueStorage(t, "u64", word64{a: -1, b: 2}, word64{a: 3, b: -4})
		testValueStorage(t, "u64", [5]byte{1, 2, 3, 4, 5}, [5]byte{6, 7, 8, 9, 10})
		large := int64(1) << 62
		if unsafe.Sizeof(0) == 4 {
			large = 1 << 30
		}
		testValueStorage(t, "u64", -7, int(large))
	})
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		testValueStorage(t, "empty", struct{}{}, struct{}{})
		testValueStorage(t, "empty", [0]int{}, [0]int{})
	})
	t.Run("inline", func(t *testing.T) {
		t.Parallel()
		testValueStorage(t, "", "a", "b")
		testValueStorage(t, "", []int{1}, []int{2})
		testValueStorage(t, "", any(1), any("b"))
		testValueStorage(t, "", withPointer{p: &x}, withPointer{p: &y})
		testValueStorage(t, "", complex128(1+2i), complex128(3+4i))
		testValueStorage(t, "", [9]byte{1}, [9]byte{2})
	})
}

// Every node type is the shared header followed by the value field alone: a node that has not
// been updated carries no box pointer, a boxed node no inline copy of the value, and a value kept
// in one atomic word neither.
func TestCache_NodeSize(t *testing.T) {
	t.Parallel()

	type large [64]byte
	pointerSize := unsafe.Sizeof(uintptr(0))
	boxed := unsafe.Sizeof(node.BSBoxed[int, large]{})
	header := boxed - pointerSize

	require.Equal(t, header+unsafe.Sizeof(large{}), unsafe.Sizeof(node.BSInline[int, large]{}))
	require.Equal(t, boxed, unsafe.Sizeof(node.BSP[int, *int]{}))
	// a value of zero size is not stored: such a node is allocated as the header alone
	require.Equal(t, header, unsafe.Sizeof(node.BS[int, struct{}]{}))
	require.Equal(t, unsafe.Sizeof(node.BSInline[int, *int]{}), unsafe.Sizeof(node.BSP[int, *int]{}))
	if pointerSize == 8 {
		require.Equal(t, unsafe.Sizeof(node.BSInline[int, uint64]{}), unsafe.Sizeof(node.BSU64[int, uint64]{}))
	} else {
		// A plain uint64 is only 4-byte aligned on 32-bit platforms, while atomic.Uint64 is
		// always 8-byte aligned, which may cost padding.
		require.LessOrEqual(t, unsafe.Sizeof(node.BSU64[int, uint64]{}), unsafe.Sizeof(node.BSInline[int, uint64]{})+4)
	}
}

// Reads never observe a value that mixes two writes.
func TestCache_WordStorageConcurrentReads(t *testing.T) {
	t.Parallel()

	type pair struct {
		a, b int32
	}
	type box struct {
		a, b int
	}

	testConcurrentReads(t, func(i int) pair { return pair{a: int32(i), b: int32(i)} }, func(v pair) bool { return v.a == v.b })
	testConcurrentReads(t, func(i int) *box { return &box{a: i, b: i} }, func(v *box) bool { return v.a == v.b })
}

func testConcurrentReads[V any](t *testing.T, newValue func(i int) V, valid func(v V) bool) {
	t.Helper()

	c := Must(&Options[int, V]{
		MaximumSize:      1000,
		ExpiryCalculator: ExpiryAccessing[int, V](time.Hour),
	})
	const keys = 16
	for i := 0; i < keys; i++ {
		c.Set(i, newValue(0))
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				for i := 0; i < keys; i++ {
					if v, ok := c.GetIfPresent(i); ok && !valid(v) {
						t.Errorf("torn value: %+v", v)
						return
					}
				}
			}
		}()
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 1; j <= 5000; j++ {
				c.Set((j+w)%keys, newValue(j))
			}
		}(w)
	}
	time.Sleep(100 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}
