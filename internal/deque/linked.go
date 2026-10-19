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
	head  node.Node
	tail  node.Node
	len   int
	isExp bool
}

func NewLinked(isExp bool) *Linked {
	return &Linked{
		isExp: isExp,
	}
}

func (d *Linked) PushBack(n node.Node) {
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

func (d *Linked) UpdateNode(n, old node.Node) {
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

func (d *Linked) PushFront(n node.Node) {
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

func (d *Linked) PopFront() node.Node {
	if d.IsEmpty() {
		return nil
	}

	result := d.head
	d.Delete(result)
	return result
}

/*
func (d *Linked) PopBack() node.Node {
	if d.IsEmpty() {
		return nil
	}

	result := d.tail
	d.Delete(result)
	return result
}
*/

func (d *Linked) NotContains(n node.Node) bool {
	return !d.Contains(n)
}

func (d *Linked) Contains(n node.Node) bool {
	return d.getPrev(n) != nil || d.getNext(n) != nil || d.head == n
}

func (d *Linked) MoveToBack(n node.Node) {
	if n != d.tail {
		d.Delete(n)
		d.PushBack(n)
	}
}

func (d *Linked) MoveToFront(n node.Node) {
	if n != d.head {
		d.Delete(n)
		d.PushFront(n)
	}
}

func (d *Linked) Delete(n node.Node) {
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

func (d *Linked) Head() node.Node {
	return d.head
}

func (d *Linked) Tail() node.Node {
	return d.tail
}

func (d *Linked) All() iter.Seq[node.Node] {
	return func(yield func(node.Node) bool) {
		cursor := d.head
		for cursor != nil {
			if !yield(cursor) {
				return
			}
			cursor = d.getNext(cursor)
		}
	}
}

func (d *Linked) Backward() iter.Seq[node.Node] {
	return func(yield func(node.Node) bool) {
		cursor := d.tail
		for cursor != nil {
			if !yield(cursor) {
				return
			}
			cursor = d.getPrev(cursor)
		}
	}
}

func (d *Linked) setPrev(to, n node.Node) {
	if d.isExp {
		to.SetPrevExp(n)
	} else {
		to.SetPrev(n)
	}
}

func (d *Linked) setNext(to, n node.Node) {
	if d.isExp {
		to.SetNextExp(n)
	} else {
		to.SetNext(n)
	}
}

func (d *Linked) getNext(n node.Node) node.Node {
	if d.isExp {
		return n.NextExp()
	} else {
		return n.Next()
	}
}

func (d *Linked) getPrev(n node.Node) node.Node {
	if d.isExp {
		return n.PrevExp()
	} else {
		return n.Prev()
	}
}
