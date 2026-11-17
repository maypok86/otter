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

	"github.com/maypok86/otter/v2/internal/generated/node"
)

type Linked struct {
	kind  *node.Kind
	head  *node.Header
	tail  *node.Header
	len   int
	isExp bool
}

func NewLinked(kind *node.Kind, isExp bool) *Linked {
	return &Linked{
		kind:  kind,
		isExp: isExp,
	}
}

func (d *Linked) PushBack(n *node.Header) {
	if d.IsEmpty() {
		d.head = n
		d.tail = n
	} else {
		d.setPrev(n, d.tail)
		d.setNext(d.tail, n)
		d.tail = n
	}

	d.len++
}

func (d *Linked) UpdateNode(n, old *node.Header) {
	oldNext := d.getNext(old)
	if oldNext == nil {
		if d.tail == old {
			d.tail = n
		}
	} else {
		d.setPrev(oldNext, n)
		d.setNext(n, oldNext)
		d.setNext(old, nil)
	}

	oldPrev := d.getPrev(old)
	if oldPrev == nil {
		if d.head == old {
			d.head = n
		}
	} else {
		d.setPrev(n, oldPrev)
		d.setNext(oldPrev, n)
		d.setPrev(old, nil)
	}
}

func (d *Linked) PushFront(n *node.Header) {
	if d.IsEmpty() {
		d.head = n
		d.tail = n
	} else {
		d.setNext(n, d.head)
		d.setPrev(d.head, n)
		d.head = n
	}

	d.len++
}

func (d *Linked) PopFront() *node.Header {
	if d.IsEmpty() {
		return nil
	}

	result := d.head
	d.Delete(result)
	return result
}

func (d *Linked) NotContains(n *node.Header) bool {
	return !d.Contains(n)
}

func (d *Linked) Contains(n *node.Header) bool {
	return d.getPrev(n) != nil || d.getNext(n) != nil || d.head == n
}

func (d *Linked) MoveToBack(n *node.Header) {
	if n != d.tail {
		d.Delete(n)
		d.PushBack(n)
	}
}

func (d *Linked) MoveToFront(n *node.Header) {
	if n != d.head {
		d.Delete(n)
		d.PushFront(n)
	}
}

func (d *Linked) Delete(n *node.Header) {
	next := d.getNext(n)
	prev := d.getPrev(n)

	if prev == nil {
		if next == nil && d.head != n {
			return
		}

		d.head = next
	} else {
		d.setNext(prev, next)
		d.setPrev(n, nil)
	}

	if next == nil {
		d.tail = prev
	} else {
		d.setPrev(next, prev)
		d.setNext(n, nil)
	}

	d.len--
}

func (d *Linked) Clear() {
	for !d.IsEmpty() {
		d.PopFront()
	}
}

func (d *Linked) Len() int {
	return d.len
}

func (d *Linked) IsEmpty() bool {
	return d.Len() == 0
}

func (d *Linked) Head() *node.Header {
	return d.head
}

func (d *Linked) Tail() *node.Header {
	return d.tail
}

func (d *Linked) All() iter.Seq[*node.Header] {
	return func(yield func(*node.Header) bool) {
		cursor := d.head
		for cursor != nil {
			if !yield(cursor) {
				return
			}
			cursor = d.getNext(cursor)
		}
	}
}

func (d *Linked) Backward() iter.Seq[*node.Header] {
	return func(yield func(*node.Header) bool) {
		cursor := d.tail
		for cursor != nil {
			if !yield(cursor) {
				return
			}
			cursor = d.getPrev(cursor)
		}
	}
}

func (d *Linked) setPrev(to, n *node.Header) {
	h := d.kind.Header(to)
	if d.isExp {
		h.SetPrevExp(n)
	} else {
		h.SetPrev(n)
	}
}

func (d *Linked) setNext(to, n *node.Header) {
	h := d.kind.Header(to)
	if d.isExp {
		h.SetNextExp(n)
	} else {
		h.SetNext(n)
	}
}

func (d *Linked) getNext(n *node.Header) *node.Header {
	h := d.kind.Header(n)
	if d.isExp {
		return h.NextExp()
	}
	return h.Next()
}

func (d *Linked) getPrev(n *node.Header) *node.Header {
	h := d.kind.Header(n)
	if d.isExp {
		return h.PrevExp()
	}
	return h.Prev()
}

// KeyedLinked is a strictly typed *node.Keyed[K] facade over the non-generic Linked deque.
type KeyedLinked[K comparable] struct {
	raw  *Linked
	lens node.KeyLens[K]
}

func NewKeyedLinked[K comparable](kind *node.Kind, lens node.KeyLens[K], isExp bool) *KeyedLinked[K] {
	return &KeyedLinked[K]{
		raw:  NewLinked(kind, isExp),
		lens: lens,
	}
}

func (d *KeyedLinked[K]) PushBack(n *node.Keyed[K])        { d.raw.PushBack(n.Header()) }
func (d *KeyedLinked[K]) PushFront(n *node.Keyed[K])       { d.raw.PushFront(n.Header()) }
func (d *KeyedLinked[K]) UpdateNode(n, old *node.Keyed[K]) { d.raw.UpdateNode(n.Header(), old.Header()) }
func (d *KeyedLinked[K]) PopFront() *node.Keyed[K]         { return d.lens.FromHeader(d.raw.PopFront()) }
func (d *KeyedLinked[K]) NotContains(n *node.Keyed[K]) bool {
	return d.raw.NotContains(n.Header())
}
func (d *KeyedLinked[K]) Contains(n *node.Keyed[K]) bool { return d.raw.Contains(n.Header()) }
func (d *KeyedLinked[K]) MoveToBack(n *node.Keyed[K])    { d.raw.MoveToBack(n.Header()) }
func (d *KeyedLinked[K]) MoveToFront(n *node.Keyed[K])   { d.raw.MoveToFront(n.Header()) }
func (d *KeyedLinked[K]) Delete(n *node.Keyed[K])       { d.raw.Delete(n.Header()) }
func (d *KeyedLinked[K]) Clear()                         { d.raw.Clear() }
func (d *KeyedLinked[K]) Len() int                       { return d.raw.Len() }
func (d *KeyedLinked[K]) IsEmpty() bool                  { return d.raw.IsEmpty() }
func (d *KeyedLinked[K]) Head() *node.Keyed[K]           { return d.lens.FromHeader(d.raw.Head()) }
func (d *KeyedLinked[K]) Tail() *node.Keyed[K]           { return d.lens.FromHeader(d.raw.Tail()) }

func (d *KeyedLinked[K]) All() iter.Seq[*node.Keyed[K]] {
	lens := d.lens
	return func(yield func(*node.Keyed[K]) bool) {
		for h := range d.raw.All() {
			if !yield(lens.FromHeader(h)) {
				return
			}
		}
	}
}

func (d *KeyedLinked[K]) Backward() iter.Seq[*node.Keyed[K]] {
	lens := d.lens
	return func(yield func(*node.Keyed[K]) bool) {
		for h := range d.raw.Backward() {
			if !yield(lens.FromHeader(h)) {
				return
			}
		}
	}
}
