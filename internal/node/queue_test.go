// Copyright notice. Initial version of the following tests was based on
// the following file from the Go Programming Language core repo:
// https://cs.opensource.google/go/go/+/refs/tags/go1.21.5:src/container/list/list_test.go
package node

import (
	"strconv"
	"testing"
)

func checkQueueLen[K comparable, V any](t *testing.T, q *Queue[K, V], length int) bool {
	t.Helper()

	if n := q.Len(); n != length {
		t.Errorf("q.Len() = %d, want %d", n, length)
		return false
	}
	return true
}

func checkQueuePointers[K comparable, V any](t *testing.T, q *Queue[K, V], nodes []*Node[K, V]) {
	t.Helper()

	if !checkQueueLen(t, q, len(nodes)) {
		return
	}

	// zero length queues must be the zero value
	if len(nodes) == 0 {
		if !(q.head == nil && q.tail == nil) {
			t.Errorf("q.head = %p, q.tail = %p; both should be nil", q.head, q.tail)
		}
		return
	}

	// check internal and external prev/next connections
	for i, n := range nodes {
		prev := (*Node[K, V])(nil)
		if i > 0 {
			prev = nodes[i-1]
		}
		if p := n.prev; p != prev {
			t.Errorf("elt[%d](%p).prev = %p, want %p", i, n, p, prev)
		}

		next := (*Node[K, V])(nil)
		if i < len(nodes)-1 {
			next = nodes[i+1]
		}
		if nn := n.next; nn != next {
			t.Errorf("nodes[%d](%p).next = %p, want %p", i, n, nn, next)
		}
	}
}

func newFakeNode[K comparable](e K) *Node[K, K] {
	return New(e, e, 0, 0)
}

func TestQueue(t *testing.T) {
	q := NewQueue[string, string]()
	checkQueuePointers(t, q, []*Node[string, string]{})

	// Single element queue
	e := newFakeNode("a")
	q.Push(e)
	checkQueuePointers(t, q, []*Node[string, string]{e})
	q.Remove(e)
	q.Push(e)
	checkQueuePointers(t, q, []*Node[string, string]{e})
	q.Remove(e)
	checkQueuePointers(t, q, []*Node[string, string]{})

	// Bigger queue
	e2 := newFakeNode("2")
	e1 := newFakeNode("1")
	e3 := newFakeNode("3")
	e4 := newFakeNode("4")
	q.Push(e1)
	q.Push(e2)
	q.Push(e3)
	q.Push(e4)
	checkQueuePointers(t, q, []*Node[string, string]{e1, e2, e3, e4})

	q.Remove(e2)
	checkQueuePointers(t, q, []*Node[string, string]{e1, e3, e4})

	// move from middle
	q.Remove(e3)
	q.Push(e3)
	checkQueuePointers(t, q, []*Node[string, string]{e1, e4, e3})

	q.Clear()
	q.Push(e3)
	q.Push(e1)
	q.Push(e4)
	checkQueuePointers(t, q, []*Node[string, string]{e3, e1, e4})

	// should be no-op
	q.Remove(e3)
	q.Push(e3)
	checkQueuePointers(t, q, []*Node[string, string]{e1, e4, e3})

	// Check standard iteration.
	sum := 0
	for e := q.head; e != nil; e = e.next {
		i, err := strconv.Atoi(e.value)
		if err != nil {
			continue
		}
		sum += i
	}
	if sum != 8 {
		t.Errorf("sum over l = %d, want 8", sum)
	}

	// Clear all elements by iterating
	var next *Node[string, string]
	for e := q.head; e != nil; e = next {
		next = e.next
		q.Remove(e)
	}
	checkQueuePointers(t, q, []*Node[string, string]{})
}

func TestQueue_Remove(t *testing.T) {
	q := NewQueue[int, int]()

	e1 := newFakeNode(1)
	e2 := newFakeNode(2)
	q.Push(e1)
	q.Push(e2)
	checkQueuePointers(t, q, []*Node[int, int]{e1, e2})
	e := q.head
	q.Remove(e)
	checkQueuePointers(t, q, []*Node[int, int]{e2})
	q.Remove(e)
	checkQueuePointers(t, q, []*Node[int, int]{e2})
}
