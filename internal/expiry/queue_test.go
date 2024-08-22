// Copyright (c) 2024 Alexey Mayshev. All rights reserved.
// Copyright 2009 The Go Authors. All rights reserved.
//
// Copyright notice. Initial version of the following tests was based on
// the following file from the Go Programming Language core repo:
// https://cs.opensource.google/go/go/+/refs/tags/go1.21.5:src/container/list/list_test.go
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// That can be found at https://cs.opensource.google/go/go/+/refs/tags/go1.21.5:LICENSE

package expiry

import (
	"strconv"
	"testing"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

func checkQueueLen[K comparable, V any](t *testing.T, q *queue[K, V], length int) bool {
	t.Helper()

	if n := q.length(); n != length {
		t.Errorf("q.length() = %d, want %d", n, length)
		return false
	}
	return true
}

func checkQueuePointers[K comparable, V any](t *testing.T, q *queue[K, V], nodes []node.Node[K, V]) {
	t.Helper()

	if !checkQueueLen(t, q, len(nodes)) {
		return
	}

	// zero length queues must be the zero value
	if len(nodes) == 0 {
		if !(node.Equals(q.head, nil) && node.Equals(q.tail, nil)) {
			t.Errorf("q.head = %p, q.tail = %p; both should be nil", q.head, q.tail)
		}
		return
	}

	// check internal and external prev/next connections
	for i, n := range nodes {
		var prev node.Node[K, V]
		if i > 0 {
			prev = nodes[i-1]
		}
		if p := n.PrevExp(); !node.Equals(p, prev) {
			t.Errorf("elt[%d](%p).prev = %p, want %p", i, n, p, prev)
		}

		var next node.Node[K, V]
		if i < len(nodes)-1 {
			next = nodes[i+1]
		}
		if nn := n.NextExp(); !node.Equals(nn, next) {
			t.Errorf("nodes[%d](%p).next = %p, want %p", i, n, nn, next)
		}
	}
}

func newNode[K comparable](e K) node.Node[K, K] {
	m := node.NewManager[K, K](node.Config{WithWeight: true, WithExpiration: true})
	return m.Create(e, e, 0, 0)
}

func TestQueue(t *testing.T) {
	q := newQueue[string, string]()
	checkQueuePointers(t, q, []node.Node[string, string]{})

	// Single element queue
	e := newNode("a")
	q.push(e)
	checkQueuePointers(t, q, []node.Node[string, string]{e})
	q.delete(e)
	q.push(e)
	checkQueuePointers(t, q, []node.Node[string, string]{e})
	q.delete(e)
	checkQueuePointers(t, q, []node.Node[string, string]{})

	// Bigger queue
	e2 := newNode("2")
	e1 := newNode("1")
	e3 := newNode("3")
	e4 := newNode("4")
	q.push(e1)
	q.push(e2)
	q.push(e3)
	q.push(e4)
	checkQueuePointers(t, q, []node.Node[string, string]{e1, e2, e3, e4})

	q.delete(e2)
	checkQueuePointers(t, q, []node.Node[string, string]{e1, e3, e4})

	// move from middle
	q.delete(e3)
	q.push(e3)
	checkQueuePointers(t, q, []node.Node[string, string]{e1, e4, e3})

	q.clear()
	q.push(e3)
	q.push(e1)
	q.push(e4)
	checkQueuePointers(t, q, []node.Node[string, string]{e3, e1, e4})

	// should be no-op
	q.delete(e3)
	q.push(e3)
	checkQueuePointers(t, q, []node.Node[string, string]{e1, e4, e3})

	// Check standard iteration.
	sum := 0
	for e := q.head; !node.Equals(e, nil); e = e.NextExp() {
		i, err := strconv.Atoi(e.Value())
		if err != nil {
			continue
		}
		sum += i
	}
	if sum != 8 {
		t.Errorf("sum over l = %d, want 8", sum)
	}

	// Clear all elements by iterating
	var next node.Node[string, string]
	for e := q.head; !node.Equals(e, nil); e = next {
		next = e.NextExp()
		q.delete(e)
	}
	checkQueuePointers(t, q, []node.Node[string, string]{})
}

func TestQueue_Remove(t *testing.T) {
	q := newQueue[int, int]()

	e1 := newNode(1)
	e2 := newNode(2)
	q.push(e1)
	q.push(e2)
	checkQueuePointers(t, q, []node.Node[int, int]{e1, e2})
	e := q.head
	q.delete(e)
	checkQueuePointers(t, q, []node.Node[int, int]{e2})
	q.delete(e)
	checkQueuePointers(t, q, []node.Node[int, int]{e2})
}
