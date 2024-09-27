// Copyright (c) 2024 Alexey Mayshev and contributors. All rights reserved.
// Copyright (c) 2009 The Go Authors. All rights reserved.
//
// Copyright notice. Initial version of the following tests was based on
// the following file from the Go Programming Language core repo:
// https://cs.opensource.google/go/go/+/refs/tags/go1.21.5:src/container/list/list_test.go
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// That can be found at https://cs.opensource.google/go/go/+/refs/tags/go1.21.5:LICENSE

package deque

import (
	"strconv"
	"testing"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

func checkLinkedLen[K comparable, V any](t *testing.T, d *Linked[K, V], length int) bool {
	t.Helper()

	if n := d.Len(); n != length {
		t.Errorf("d.Len() = %d, want %d", n, length)
		return false
	}
	return true
}

func checkLinkedPtrs[K comparable, V any](t *testing.T, d *Linked[K, V], nodes []node.Node[K, V]) {
	t.Helper()

	if !checkLinkedLen(t, d, len(nodes)) {
		return
	}

	// zero length queues must be the zero value
	if len(nodes) == 0 {
		if !(node.Equals(d.head, nil) && node.Equals(d.tail, nil)) {
			t.Errorf("d.head = %p, d.tail = %p; both should be nil", d.head, d.tail)
		}
		return
	}

	// check internal and external prev/next connections
	for i, n := range nodes {
		var prev node.Node[K, V]
		if i > 0 {
			prev = nodes[i-1]
		}
		if p := d.getPrev(n); !node.Equals(p, prev) {
			t.Errorf("elt[%d](%p).prev = %p, want %p", i, n, p, prev)
		}

		var next node.Node[K, V]
		if i < len(nodes)-1 {
			next = nodes[i+1]
		}
		if nn := d.getNext(n); !node.Equals(nn, next) {
			t.Errorf("nodes[%d](%p).next = %p, want %p", i, n, nn, next)
		}
	}
}

func newNode[K comparable](e K) node.Node[K, K] {
	m := node.NewManager[K, K](node.Config{WithWeight: true, WithExpiration: true})
	return m.Create(e, e, 0, 0)
}

func TestLinked(t *testing.T) {
	d := NewLinked[string, string](false)
	checkLinkedPtrs(t, d, []node.Node[string, string]{})

	// Single element Linked
	e := newNode("a")
	d.PushBack(e)
	checkLinkedPtrs(t, d, []node.Node[string, string]{e})
	d.Delete(e)
	d.PushBack(e)
	checkLinkedPtrs(t, d, []node.Node[string, string]{e})
	d.Delete(e)
	checkLinkedPtrs(t, d, []node.Node[string, string]{})

	// Bigger Linked
	e2 := newNode("2")
	e1 := newNode("1")
	e3 := newNode("3")
	e4 := newNode("4")
	d.PushBack(e1)
	d.PushBack(e2)
	d.PushBack(e3)
	d.PushBack(e4)
	checkLinkedPtrs(t, d, []node.Node[string, string]{e1, e2, e3, e4})

	d.Delete(e2)
	checkLinkedPtrs(t, d, []node.Node[string, string]{e1, e3, e4})

	// move from middle
	d.Delete(e3)
	d.PushBack(e3)
	checkLinkedPtrs(t, d, []node.Node[string, string]{e1, e4, e3})

	d.Clear()
	d.PushBack(e3)
	d.PushBack(e1)
	d.PushBack(e4)
	checkLinkedPtrs(t, d, []node.Node[string, string]{e3, e1, e4})

	// should be no-op
	d.Delete(e3)
	d.PushBack(e3)
	checkLinkedPtrs(t, d, []node.Node[string, string]{e1, e4, e3})

	// Check standard iteration.
	sum := 0
	for e := d.head; !node.Equals(e, nil); e = d.getNext(e) {
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
	for e := d.head; !node.Equals(e, nil); e = next {
		next = d.getNext(e)
		d.Delete(e)
	}
	checkLinkedPtrs(t, d, []node.Node[string, string]{})
}

func TestLinked_Delete(t *testing.T) {
	d := NewLinked[int, int](true)

	e1 := newNode(1)
	e2 := newNode(2)
	d.PushBack(e1)
	d.PushBack(e2)
	checkLinkedPtrs(t, d, []node.Node[int, int]{e1, e2})
	e := d.head
	d.Delete(e)
	checkLinkedPtrs(t, d, []node.Node[int, int]{e2})
	d.Delete(e)
	checkLinkedPtrs(t, d, []node.Node[int, int]{e2})
}
