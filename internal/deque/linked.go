// Copyright (c) 2024 Alexey Mayshev. All rights reserved.
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
	"github.com/maypok86/otter/v2/internal/generated/node"
)

type Linked[K comparable, V any] struct {
	head  node.Node[K, V]
	tail  node.Node[K, V]
	len   int
	isExp bool
}

func NewLinked[K comparable, V any](isExp bool) *Linked[K, V] {
	return &Linked[K, V]{
		isExp: isExp,
	}
}

func (d *Linked[K, V]) PushBack(n node.Node[K, V]) {
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

func (d *Linked[K, V]) PushFront(n node.Node[K, V]) {
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

func (d *Linked[K, V]) PopFront() node.Node[K, V] {
	if d.IsEmpty() {
		return nil
	}

	result := d.head
	d.Delete(result)
	return result
}

func (d *Linked[K, V]) PopBack() node.Node[K, V] {
	if d.IsEmpty() {
		return nil
	}

	result := d.tail
	d.Delete(result)
	return result
}

func (d *Linked[K, V]) Delete(n node.Node[K, V]) {
	next := d.getNext(n)
	prev := d.getPrev(n)

	if node.Equals(prev, nil) {
		if node.Equals(next, nil) && !node.Equals(d.head, n) {
			return
		}

		d.head = next
	} else {
		d.setNext(prev, next)
		d.setPrev(n, nil)
	}

	if node.Equals(next, nil) {
		d.tail = prev
	} else {
		d.setPrev(next, prev)
		d.setNext(n, nil)
	}

	d.len--
}

func (d *Linked[K, V]) Clear() {
	for !d.IsEmpty() {
		d.PopFront()
	}
}

func (d *Linked[K, V]) Len() int {
	return d.len
}

func (d *Linked[K, V]) IsEmpty() bool {
	return d.Len() == 0
}

func (d *Linked[K, V]) Head() node.Node[K, V] {
	return d.head
}

func (d *Linked[K, V]) Tail() node.Node[K, V] {
	return d.tail
}

func (d *Linked[K, V]) setPrev(to, n node.Node[K, V]) {
	if d.isExp {
		to.SetPrevExp(n)
	} else {
		to.SetPrev(n)
	}
}

func (d *Linked[K, V]) setNext(to, n node.Node[K, V]) {
	if d.isExp {
		to.SetNextExp(n)
	} else {
		to.SetNext(n)
	}
}

func (d *Linked[K, V]) getNext(n node.Node[K, V]) node.Node[K, V] {
	if d.isExp {
		return n.NextExp()
	} else {
		return n.Next()
	}
}

func (d *Linked[K, V]) getPrev(n node.Node[K, V]) node.Node[K, V] {
	if d.isExp {
		return n.PrevExp()
	} else {
		return n.Prev()
	}
}