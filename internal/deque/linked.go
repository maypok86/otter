// Copyright (c) 2024 Alexey Mayshev and contributors. All rights reserved.
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

package deque

import (
	"iter"
	"unsafe"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

// Linked is an intrusive doubly-linked list of nodes. It follows the nodes' links through raw
// pointers at the offset the node manager reports, so linking and unlinking do not call the
// nodes' methods; a node.Node is made only for the nodes it hands out.
type Linked[K comparable, V any] struct {
	head        unsafe.Pointer
	tail        unsafe.Pointer
	len         int
	offset      uintptr
	nodeManager *node.Manager[K, V]
}

func NewLinked[K comparable, V any](nodeManager *node.Manager[K, V], isExp bool) *Linked[K, V] {
	var offset uintptr
	if isExp {
		offset = nodeManager.ExpLinksOffset()
	} else {
		offset = nodeManager.LinksOffset()
	}
	return &Linked[K, V]{
		offset:      offset,
		nodeManager: nodeManager,
	}
}

func (d *Linked[K, V]) links(p unsafe.Pointer) *node.Links {
	return (*node.Links)(unsafe.Add(p, d.offset))
}

func (d *Linked[K, V]) PushBack(n node.Node[K, V]) {
	d.pushBack(n.AsPointer())
}

func (d *Linked[K, V]) pushBack(p unsafe.Pointer) {
	if d.IsEmpty() {
		d.head = p
		d.tail = p
	} else {
		d.links(p).Prev = d.tail
		d.links(d.tail).Next = p
		d.tail = p
	}

	d.len++
}

func (d *Linked[K, V]) UpdateNode(n, old node.Node[K, V]) {
	p := n.AsPointer()
	o := old.AsPointer()
	nl := d.links(p)
	ol := d.links(o)

	if ol.Next == nil {
		if d.tail == o {
			d.tail = p
		}
	} else {
		d.links(ol.Next).Prev = p
		nl.Next = ol.Next
		ol.Next = nil
	}

	if ol.Prev == nil {
		if d.head == o {
			d.head = p
		}
	} else {
		d.links(ol.Prev).Next = p
		nl.Prev = ol.Prev
		ol.Prev = nil
	}
}

func (d *Linked[K, V]) PushFront(n node.Node[K, V]) {
	d.pushFront(n.AsPointer())
}

func (d *Linked[K, V]) pushFront(p unsafe.Pointer) {
	if d.IsEmpty() {
		d.head = p
		d.tail = p
	} else {
		d.links(p).Next = d.head
		d.links(d.head).Prev = p
		d.head = p
	}

	d.len++
}

func (d *Linked[K, V]) PopFront() node.Node[K, V] {
	if d.IsEmpty() {
		return nil
	}

	result := d.head
	d.delete(result)
	return d.nodeManager.FromPointer(result)
}

func (d *Linked[K, V]) NotContains(n node.Node[K, V]) bool {
	return !d.Contains(n)
}

func (d *Linked[K, V]) Contains(n node.Node[K, V]) bool {
	p := n.AsPointer()
	l := d.links(p)
	return l.Prev != nil || l.Next != nil || d.head == p
}

func (d *Linked[K, V]) MoveToBack(n node.Node[K, V]) {
	p := n.AsPointer()
	if p != d.tail {
		d.delete(p)
		d.pushBack(p)
	}
}

func (d *Linked[K, V]) MoveToFront(n node.Node[K, V]) {
	p := n.AsPointer()
	if p != d.head {
		d.delete(p)
		d.pushFront(p)
	}
}

func (d *Linked[K, V]) Delete(n node.Node[K, V]) {
	d.delete(n.AsPointer())
}

func (d *Linked[K, V]) delete(p unsafe.Pointer) {
	l := d.links(p)
	next := l.Next
	prev := l.Prev

	if prev == nil {
		if next == nil && d.head != p {
			return
		}

		d.head = next
	} else {
		d.links(prev).Next = next
		l.Prev = nil
	}

	if next == nil {
		d.tail = prev
	} else {
		d.links(next).Prev = prev
		l.Next = nil
	}

	d.len--
}

func (d *Linked[K, V]) Clear() {
	for !d.IsEmpty() {
		d.delete(d.head)
	}
}

func (d *Linked[K, V]) Len() int {
	return d.len
}

func (d *Linked[K, V]) IsEmpty() bool {
	return d.Len() == 0
}

func (d *Linked[K, V]) Head() node.Node[K, V] {
	return d.nodeManager.FromPointer(d.head)
}

func (d *Linked[K, V]) Tail() node.Node[K, V] {
	return d.nodeManager.FromPointer(d.tail)
}

func (d *Linked[K, V]) All() iter.Seq[node.Node[K, V]] {
	return func(yield func(node.Node[K, V]) bool) {
		cursor := d.head
		for cursor != nil {
			if !yield(d.nodeManager.FromPointer(cursor)) {
				return
			}
			cursor = d.links(cursor).Next
		}
	}
}

func (d *Linked[K, V]) Backward() iter.Seq[node.Node[K, V]] {
	return func(yield func(node.Node[K, V]) bool) {
		cursor := d.tail
		for cursor != nil {
			if !yield(d.nodeManager.FromPointer(cursor)) {
				return
			}
			cursor = d.links(cursor).Prev
		}
	}
}
