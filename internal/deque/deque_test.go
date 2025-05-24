// Copyright (c) 2025 Alexey Mayshev and contributors. All rights reserved.
// Copyright (c) 2018 Andrew J. Gillis. All rights reserved.
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
// Copyright notice. This code is a fork of tests for Deque from this file with some changes:
// https://github.com/gammazero/deque/blob/main/deque_test.go
//
// Use of this source code is governed by a MIT license that can be found
// at https://github.com/gammazero/deque/blob/main/LICENSE

package deque

import (
	"fmt"
	"slices"
	"testing"
	"unicode"
)

func TestEmpty(t *testing.T) {
	t.Parallel()

	q := new(Deque[string])
	if q.Len() != 0 {
		t.Error("q.Len() =", q.Len(), "expect 0")
	}
	if q.Cap() != 0 {
		t.Error("expected q.Cap() == 0")
	}
	idx := q.Index(func(item string) bool {
		return true
	})
	if idx != -1 {
		t.Error("should return -1 index for nil deque")
	}
	idx = q.RIndex(func(item string) bool {
		return true
	})
	if idx != -1 {
		t.Error("should return -1 index for nil deque")
	}
}

func TestNil(t *testing.T) {
	t.Parallel()

	var q *Deque[int]
	if q.Len() != 0 {
		t.Error("expected q.Len() == 0")
	}
	if q.Cap() != 0 {
		t.Error("expected q.Cap() == 0")
	}
	q.Rotate(5)
	idx := q.Index(func(item int) bool {
		return true
	})
	if idx != -1 {
		t.Error("should return -1 index for nil deque")
	}
	idx = q.RIndex(func(item int) bool {
		return true
	})
	if idx != -1 {
		t.Error("should return -1 index for nil deque")
	}
}

func TestFrontBack(t *testing.T) {
	t.Parallel()

	var q Deque[string]
	q.PushBack("foo")
	q.PushBack("bar")
	q.PushBack("baz")
	if q.Front() != "foo" {
		t.Error("wrong value at front of queue")
	}
	if q.Back() != "baz" {
		t.Error("wrong value at back of queue")
	}

	if q.PopFront() != "foo" {
		t.Error("wrong value removed from front of queue")
	}
	if q.Front() != "bar" {
		t.Error("wrong value remaining at front of queue")
	}
	if q.Back() != "baz" {
		t.Error("wrong value remaining at back of queue")
	}

	if q.PopBack() != "baz" {
		t.Error("wrong value removed from back of queue")
	}
	if q.Front() != "bar" {
		t.Error("wrong value remaining at front of queue")
	}
	if q.Back() != "bar" {
		t.Error("wrong value remaining at back of queue")
	}
}

func TestGrowShrinkBack(t *testing.T) {
	t.Parallel()

	var q Deque[int]
	size := minCapacity * 2

	for i := 0; i < size; i++ {
		if q.Len() != i {
			t.Error("q.Len() =", q.Len(), "expected", i)
		}
		q.PushBack(i)
	}
	bufLen := len(q.buf)

	// Remove from back.
	for i := size; i > 0; i-- {
		if q.Len() != i {
			t.Error("q.Len() =", q.Len(), "expected", i)
		}
		x := q.PopBack()
		if x != i-1 {
			t.Error("q.PopBack() =", x, "expected", i-1)
		}
	}
	if q.Len() != 0 {
		t.Error("q.Len() =", q.Len(), "expected 0")
	}
	if len(q.buf) == bufLen {
		t.Error("queue buffer did not shrink")
	}
}

func TestGrowShrinkFront(t *testing.T) {
	t.Parallel()

	var q Deque[int]
	size := minCapacity * 2

	for i := 0; i < size; i++ {
		if q.Len() != i {
			t.Error("q.Len() =", q.Len(), "expected", i)
		}
		q.PushBack(i)
	}
	bufLen := len(q.buf)

	// Remove from Front
	for i := 0; i < size; i++ {
		if q.Len() != size-i {
			t.Error("q.Len() =", q.Len(), "expected", minCapacity*2-i)
		}
		x := q.PopFront()
		if x != i {
			t.Error("q.PopBack() =", x, "expected", i)
		}
	}
	if q.Len() != 0 {
		t.Error("q.Len() =", q.Len(), "expected 0")
	}
	if len(q.buf) == bufLen {
		t.Error("queue buffer did not shrink")
	}
}

func TestSimple(t *testing.T) {
	t.Parallel()

	var q Deque[int]

	for i := 0; i < minCapacity; i++ {
		q.PushBack(i)
	}
	if q.Front() != 0 {
		t.Fatalf("expected 0 at front, got %d", q.Front())
	}
	if q.Back() != minCapacity-1 {
		t.Fatalf("expected %d at back, got %d", minCapacity-1, q.Back())
	}

	for i := 0; i < minCapacity; i++ {
		if q.Front() != i {
			t.Error("peek", i, "had value", q.Front())
		}
		x := q.PopFront()
		if x != i {
			t.Error("remove", i, "had value", x)
		}
	}

	q.Clear()
	for i := 0; i < minCapacity; i++ {
		q.PushFront(i)
	}
	for i := minCapacity - 1; i >= 0; i-- {
		x := q.PopFront()
		if x != i {
			t.Error("remove", i, "had value", x)
		}
	}
}

func TestBufferWrap(t *testing.T) {
	t.Parallel()

	var q Deque[int]

	for i := 0; i < minCapacity; i++ {
		q.PushBack(i)
	}

	for i := 0; i < 3; i++ {
		q.PopFront()
		q.PushBack(minCapacity + i)
	}

	for i := 0; i < minCapacity; i++ {
		if q.Front() != i+3 {
			t.Error("peek", i, "had value", q.Front())
		}
		q.PopFront()
	}
}

func TestBufferWrapReverse(t *testing.T) {
	t.Parallel()

	var q Deque[int]

	for i := 0; i < minCapacity; i++ {
		q.PushFront(i)
	}
	for i := 0; i < 3; i++ {
		q.PopBack()
		q.PushFront(minCapacity + i)
	}

	for i := 0; i < minCapacity; i++ {
		if q.Back() != i+3 {
			t.Error("peek", i, "had value", q.Front())
		}
		q.PopBack()
	}
}

func TestLen(t *testing.T) {
	t.Parallel()

	var q Deque[int]

	if q.Len() != 0 {
		t.Error("empty queue length not 0")
	}

	for i := 0; i < 1000; i++ {
		q.PushBack(i)
		if q.Len() != i+1 {
			t.Error("adding: queue with", i, "elements has length", q.Len())
		}
	}
	for i := 0; i < 1000; i++ {
		q.PopFront()
		if q.Len() != 1000-i-1 {
			t.Error("removing: queue with", 1000-i-i, "elements has length", q.Len())
		}
	}
}

func TestBack(t *testing.T) {
	t.Parallel()

	var q Deque[int]

	for i := 0; i < minCapacity+5; i++ {
		q.PushBack(i)
		if q.Back() != i {
			t.Errorf("Back returned wrong value")
		}
	}
}

func TestGrow(t *testing.T) {
	t.Parallel()

	var q Deque[int]
	assertPanics(t, "should panic when calling Groe with invalid size", func() {
		q.Grow(-1)
	})

	q.Grow(35)
	if q.Cap() != 64 {
		t.Fatal(t, "did not grow to expected capacity")
	}

	q.Grow(55)
	if q.Cap() != 64 {
		t.Fatal(t, "expected no capacity change")
	}

	q.Grow(77)
	if q.Cap() != 128 {
		t.Fatal(t, "did not grow to expected capacity")
	}

	for i := 0; i < 127; i++ {
		q.PushBack(i)
	}
	if q.Cap() != 128 {
		t.Fatal(t, "expected no capacity change")
	}

	q.Grow(2)
	if q.Cap() != 256 {
		t.Fatal(t, "did not grow to expected capacity")
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	minCap := 64
	q := &Deque[string]{}
	q.SetBaseCap(minCap)
	if q.Cap() != 0 {
		t.Fatal("should not have allowcated mem yet")
	}
	q.PushBack("foo")
	q.PopFront()
	if q.Len() != 0 {
		t.Fatal("Len() should return 0")
	}
	if q.Cap() != minCap {
		t.Fatalf("worng capactiy expected %d, got %d", minCap, q.Cap())
	}
	curCap := 128
	q = new(Deque[string])
	q.SetBaseCap(minCap)
	q.Grow(curCap)
	if q.Cap() != curCap {
		t.Fatalf("Cap() should return %d, got %d", curCap, q.Cap())
	}
	if q.Len() != 0 {
		t.Fatalf("Len() should return 0")
	}
	q.PushBack("foo")
	if q.Cap() != curCap {
		t.Fatalf("Cap() should return %d, got %d", curCap, q.Cap())
	}
}

func checkRotate(t *testing.T, size int) {
	var q Deque[int]
	for i := 0; i < size; i++ {
		q.PushBack(i)
	}

	for i := 0; i < q.Len(); i++ {
		x := i
		for n := 0; n < q.Len(); n++ {
			if q.At(n) != x {
				t.Fatalf("a[%d] != %d after rotate and copy", n, x)
			}
			x++
			if x == q.Len() {
				x = 0
			}
		}
		q.Rotate(1)
		if q.Back() != i {
			t.Fatal("wrong value during rotation")
		}
	}
	for i := q.Len() - 1; i >= 0; i-- {
		q.Rotate(-1)
		if q.Front() != i {
			t.Fatal("wrong value during reverse rotation")
		}
	}
}

func TestRotate(t *testing.T) {
	t.Parallel()

	checkRotate(t, 10)
	checkRotate(t, minCapacity)
	checkRotate(t, minCapacity+minCapacity/2)

	var q Deque[int]
	for i := 0; i < 10; i++ {
		q.PushBack(i)
	}
	q.Rotate(11)
	if q.Front() != 1 {
		t.Error("rotating 11 places should have been same as one")
	}
	q.Rotate(-21)
	if q.Front() != 0 {
		t.Error("rotating -21 places should have been same as one -1")
	}
	q.Rotate(q.Len())
	if q.Front() != 0 {
		t.Error("should not have rotated")
	}
	q.Clear()
	q.PushBack(0)
	q.Rotate(13)
	if q.Front() != 0 {
		t.Error("should not have rotated")
	}
}

func TestAt(t *testing.T) {
	t.Parallel()

	var q Deque[int]

	for i := 0; i < 1000; i++ {
		q.PushBack(i)
	}

	// Front to back.
	for j := 0; j < q.Len(); j++ {
		if q.At(j) != j {
			t.Errorf("index %d doesn't contain %d", j, j)
		}
	}

	// Back to front
	for j := 1; j <= q.Len(); j++ {
		if q.At(q.Len()-j) != q.Len()-j {
			t.Errorf("index %d doesn't contain %d", q.Len()-j, q.Len()-j)
		}
	}
}

func TestSet(t *testing.T) {
	t.Parallel()

	var q Deque[int]

	for i := 0; i < 1000; i++ {
		q.PushBack(i)
		q.Set(i, i+50)
	}

	// Front to back.
	for j := 0; j < q.Len(); j++ {
		if q.At(j) != j+50 {
			t.Errorf("index %d doesn't contain %d", j, j+50)
		}
	}
}

func TestClear(t *testing.T) {
	t.Parallel()

	var q Deque[int]

	for i := 0; i < 100; i++ {
		q.PushBack(i)
	}
	if q.Len() != 100 {
		t.Error("push: queue with 100 elements has length", q.Len())
	}
	capacity := len(q.buf)
	q.Clear()
	if q.Len() != 0 {
		t.Error("empty queue length not 0 after clear")
	}
	if len(q.buf) != capacity {
		t.Error("queue capacity changed after clear")
	}

	// Check that there are no remaining references after Clear()
	for i := 0; i < len(q.buf); i++ {
		if q.buf[i] != 0 {
			t.Error("queue has non-nil deleted elements after Clear()")
			break
		}
	}

	for i := 0; i < 128; i++ {
		q.PushBack(i)
	}
	q.Clear()
	// Check that there are no remaining references after Clear()
	for i := 0; i < len(q.buf); i++ {
		if q.buf[i] != 0 {
			t.Error("queue has non-nil deleted elements after Clear()")
			break
		}
	}
}

func TestIndex(t *testing.T) {
	t.Parallel()

	var q Deque[rune]
	for _, x := range "Hello, 世界" {
		q.PushBack(x)
	}
	idx := q.Index(func(item rune) bool {
		c := item
		return unicode.Is(unicode.Han, c)
	})
	if idx != 7 {
		t.Fatal("Expected index 7, got", idx)
	}
	idx = q.Index(func(item rune) bool {
		c := item
		return c == 'H'
	})
	if idx != 0 {
		t.Fatal("Expected index 0, got", idx)
	}
	idx = q.Index(func(item rune) bool {
		return false
	})
	if idx != -1 {
		t.Fatal("Expected index -1, got", idx)
	}
}

func TestRIndex(t *testing.T) {
	t.Parallel()

	var q Deque[rune]
	for _, x := range "Hello, 世界" {
		q.PushBack(x)
	}
	idx := q.RIndex(func(item rune) bool {
		c := item
		return unicode.Is(unicode.Han, c)
	})
	if idx != 8 {
		t.Fatal("Expected index 8, got", idx)
	}
	idx = q.RIndex(func(item rune) bool {
		c := item
		return c == 'H'
	})
	if idx != 0 {
		t.Fatal("Expected index 0, got", idx)
	}
	idx = q.RIndex(func(item rune) bool {
		return false
	})
	if idx != -1 {
		t.Fatal("Expected index -1, got", idx)
	}
}

func TestInsert(t *testing.T) {
	t.Parallel()

	q := new(Deque[rune])
	for _, x := range "ABCDEFG" {
		q.PushBack(x)
	}
	q.Insert(4, 'x') // ABCDxEFG
	if q.At(4) != 'x' {
		t.Error("expected x at position 4, got", q.At(4))
	}

	q.Insert(2, 'y') // AByCDxEFG
	if q.At(2) != 'y' {
		t.Error("expected y at position 2")
	}
	if q.At(5) != 'x' {
		t.Error("expected x at position 5")
	}

	q.Insert(0, 'b') // bAByCDxEFG
	if q.Front() != 'b' {
		t.Error("expected b inserted at front, got", q.Front())
	}

	q.Insert(q.Len(), 'e') // bAByCDxEFGe

	for i, x := range "bAByCDxEFGe" {
		if q.PopFront() != x {
			t.Error("expected", x, "at position", i)
		}
	}

	qs := new(Deque[string])
	qs.Grow(16)

	for i := 0; i < qs.Cap(); i++ {
		qs.PushBack(fmt.Sprint(i))
	}
	// deque: 0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15
	// buffer: [0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15]
	for i := 0; i < qs.Cap()/2; i++ {
		qs.PopFront()
	}
	// deque: 8 9 10 11 12 13 14 15
	// buffer: [_,_,_,_,_,_,_,_,8,9,10,11,12,13,14,15]
	for i := 0; i < qs.Cap()/4; i++ {
		qs.PushBack(fmt.Sprint(qs.Cap() + i))
	}
	// deque: 8 9 10 11 12 13 14 15 16 17 18 19
	// buffer: [16,17,18,19,_,_,_,_,8,9,10,11,12,13,14,15]

	at := qs.Len() - 2
	qs.Insert(at, "x")
	// deque: 8 9 10 11 12 13 14 15 16 17 x 18 19
	// buffer: [16,17,x,18,19,_,_,_,8,9,10,11,12,13,14,15]
	if qs.At(at) != "x" {
		t.Error("expected x at position", at)
	}
	if qs.At(at) != "x" {
		t.Error("expected x at position", at)
	}

	qs.Insert(2, "y")
	// deque: 8 9 y 10 11 12 13 14 15 16 17 x 18 19
	// buffer: [16,17,x,18,19,_,_,8,9,y,10,11,12,13,14,15]
	if qs.At(2) != "y" {
		t.Error("expected y at position 2")
	}
	if qs.At(at+1) != "x" {
		t.Error("expected x at position 5")
	}

	qs.Insert(0, "b")
	// deque: b 8 9 y 10 11 12 13 14 15 16 17 x 18 19
	// buffer: [16,17,x,18,19,_,b,8,9,y,10,11,12,13,14,15]
	if qs.Front() != "b" {
		t.Error("expected b inserted at front, got", qs.Front())
	}

	qs.Insert(qs.Len(), "e")
	if qs.Cap() != qs.Len() {
		t.Fatal("Expected full buffer")
	}
	// deque: b 8 9 y 10 11 12 13 14 15 16 17 x 18 19 e
	// buffer: [16,17,x,18,19,e,b,8,9,y,10,11,12,13,14,15]
	for i, x := range []string{"16", "17", "x", "18", "19", "e", "b", "8", "9", "y", "10", "11", "12", "13", "14", "15"} {
		if qs.buf[i] != x {
			t.Error("expected", x, "at buffer position", i)
		}
	}
	for i, x := range []string{"b", "8", "9", "y", "10", "11", "12", "13", "14", "15", "16", "17", "x", "18", "19", "e"} {
		if qs.Front() != x {
			t.Error("expected", x, "at position", i, "got", qs.Front())
		}
		qs.PopFront()
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()

	q := new(Deque[rune])
	for _, x := range "ABCDEFG" {
		q.PushBack(x)
	}

	if q.Remove(4) != 'E' { // ABCDFG
		t.Error("expected E from position 4")
	}

	if q.Remove(2) != 'C' { // ABDFG
		t.Error("expected C at position 2")
	}
	if q.Back() != 'G' {
		t.Error("expected G at back")
	}

	if q.Remove(0) != 'A' { // BDFG
		t.Error("expected to remove A from front")
	}
	if q.Front() != 'B' {
		t.Error("expected G at back")
	}

	if q.Remove(q.Len()-1) != 'G' { // BDF
		t.Error("expected to remove G from back")
	}
	if q.Back() != 'F' {
		t.Error("expected F at back")
	}

	if q.Len() != 3 {
		t.Error("wrong length")
	}
}

func TestSwap(t *testing.T) {
	t.Parallel()

	var q Deque[string]

	q.PushBack("a")
	q.PushBack("b")
	q.PushBack("c")
	q.PushBack("d")
	q.PushBack("e")

	q.Swap(0, 4)
	if q.Front() != "e" {
		t.Fatal("wrong value at front")
	}
	if q.Back() != "a" {
		t.Fatal("wrong value at back")
	}
	q.Swap(3, 1)
	if q.At(1) != "d" {
		t.Fatal("wrong value at index 1")
	}
	if q.At(3) != "b" {
		t.Fatal("wrong value at index 3")
	}
	q.Swap(2, 2)
	if q.At(2) != "c" {
		t.Fatal("wrong value at index 2")
	}

	assertPanics(t, "should panic when removing out of range", func() {
		q.Swap(1, 5)
	})
	assertPanics(t, "should panic when removing out of range", func() {
		q.Swap(5, 1)
	})
}

func TestFrontBackOutOfRangePanics(t *testing.T) {
	t.Parallel()

	const msg = "should panic when peeking empty queue"
	var q Deque[int]
	assertPanics(t, msg, func() {
		q.Front()
	})
	assertPanics(t, msg, func() {
		q.Back()
	})

	q.PushBack(1)
	q.PopFront()

	assertPanics(t, msg, func() {
		q.Front()
	})
	assertPanics(t, msg, func() {
		q.Back()
	})
}

func TestPopFrontOutOfRangePanics(t *testing.T) {
	t.Parallel()

	var q Deque[int]

	assertPanics(t, "should panic when removing empty queue", func() {
		q.PopFront()
	})

	q.PushBack(1)
	q.PopFront()

	assertPanics(t, "should panic when removing emptied queue", func() {
		q.PopFront()
	})
}

func TestPopBackOutOfRangePanics(t *testing.T) {
	t.Parallel()

	var q Deque[int]

	assertPanics(t, "should panic when removing empty queue", func() {
		q.PopBack()
	})

	q.PushBack(1)
	q.PopBack()

	assertPanics(t, "should panic when removing emptied queue", func() {
		q.PopBack()
	})
}

func TestAtOutOfRangePanics(t *testing.T) {
	t.Parallel()

	var q Deque[int]

	q.PushBack(1)
	q.PushBack(2)
	q.PushBack(3)

	assertPanics(t, "should panic when negative index", func() {
		q.At(-4)
	})

	assertPanics(t, "should panic when index greater than length", func() {
		q.At(4)
	})
}

func TestSetOutOfRangePanics(t *testing.T) {
	t.Parallel()

	var q Deque[int]

	q.PushBack(1)
	q.PushBack(2)
	q.PushBack(3)

	assertPanics(t, "should panic when negative index", func() {
		q.Set(-4, 1)
	})

	assertPanics(t, "should panic when index greater than length", func() {
		q.Set(4, 1)
	})
}

func TestInsertOutOfRangePanics(t *testing.T) {
	t.Parallel()

	q := new(Deque[string])

	q.Insert(1, "A")
	if q.Front() != "A" {
		t.Fatal("expected A at front")
	}
	if q.Back() != "A" {
		t.Fatal("expected A at back")
	}

	q.Insert(-1, "B")
	if q.Front() != "B" {
		t.Fatal("expected B at front")
	}

	q.Insert(999, "C")
	if q.Back() != "C" {
		t.Fatal("expected C at back")
	}
}

func TestRemoveOutOfRangePanics(t *testing.T) {
	t.Parallel()

	q := new(Deque[string])

	assertPanics(t, "should panic when removing from empty queue", func() {
		q.Remove(0)
	})

	q.PushBack("A")

	assertPanics(t, "should panic when removing at negative index", func() {
		q.Remove(-1)
	})

	assertPanics(t, "should panic when removing out of range", func() {
		q.Remove(1)
	})
}

func TestSetMBaseapacity(t *testing.T) {
	t.Parallel()

	var q Deque[string]
	q.SetBaseCap(200)
	q.PushBack("A")
	if q.minCap != 256 {
		t.Fatal("wrong minimum capacity")
	}
	if q.Cap() != 256 {
		t.Fatal("wrong buffer size")
	}
	q.PopBack()
	if q.minCap != 256 {
		t.Fatal("wrong minimum capacity")
	}
	if q.Cap() != 256 {
		t.Fatal("wrong buffer size")
	}
	q.SetBaseCap(0)
	if q.minCap != minCapacity {
		t.Fatal("wrong minimum capacity")
	}
}

func assertPanics(t *testing.T, name string, f func()) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("%s: didn't panic as expected", name)
		}
	}()

	f()
}

func BenchmarkPushFront(b *testing.B) {
	var q Deque[int]
	for i := 0; i < b.N; i++ {
		q.PushFront(i)
	}
}

func BenchmarkPushBack(b *testing.B) {
	var q Deque[int]
	for i := 0; i < b.N; i++ {
		q.PushBack(i)
	}
}

func BenchmarkSerial(b *testing.B) {
	var q Deque[int]
	for i := 0; i < b.N; i++ {
		q.PushBack(i)
	}
	for i := 0; i < b.N; i++ {
		q.PopFront()
	}
}

func BenchmarkSerialReverse(b *testing.B) {
	var q Deque[int]
	for i := 0; i < b.N; i++ {
		q.PushFront(i)
	}
	for i := 0; i < b.N; i++ {
		q.PopBack()
	}
}

func BenchmarkRotate(b *testing.B) {
	q := new(Deque[int])
	for i := 0; i < b.N; i++ {
		q.PushBack(i)
	}
	b.ResetTimer()
	// N complete rotations on length N - 1.
	for i := 0; i < b.N; i++ {
		q.Rotate(b.N - 1)
	}
}

func BenchmarkInsert(b *testing.B) {
	q := new(Deque[int])
	for i := 0; i < b.N; i++ {
		q.PushBack(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Insert(q.Len()/2, -i)
	}
}

func BenchmarkRemove(b *testing.B) {
	q := new(Deque[int])
	for i := 0; i < b.N; i++ {
		q.PushBack(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Remove(q.Len() / 2)
	}
}

func BenchmarkYoyo(b *testing.B) {
	var q Deque[int]
	for i := 0; i < b.N; i++ {
		for j := 0; j < 65536; j++ {
			q.PushBack(j)
		}
		for j := 0; j < 65536; j++ {
			q.PopFront()
		}
	}
}

func BenchmarkYoyoFixed(b *testing.B) {
	var q Deque[int]
	q.SetBaseCap(64000)
	for i := 0; i < b.N; i++ {
		for j := 0; j < 65536; j++ {
			q.PushBack(j)
		}
		for j := 0; j < 65536; j++ {
			q.PopFront()
		}
	}
}

func BenchmarkClearContiguous(b *testing.B) {
	var src Deque[int]
	for i := range 1<<15 + 1<<14 {
		src.PushBack(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := src
		q.buf = slices.Clone(q.buf)
		q.Clear()
	}
}

func BenchmarkClearSplit(b *testing.B) {
	var src Deque[int]
	for i := range 1<<15 + 1<<14 {
		src.PushFront(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := src
		q.buf = slices.Clone(q.buf)
		q.Clear()
	}
}
