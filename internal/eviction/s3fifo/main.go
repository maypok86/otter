// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
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

package s3fifo

import (
	"github.com/maypok86/otter/v2/internal/deque"
	"github.com/maypok86/otter/v2/internal/generated/node"
)

const maxReinsertions = 20

type main[K comparable, V any] struct {
	d         *deque.Linked[K, V]
	weight    uint64
	maxWeight uint64
}

func newMain[K comparable, V any](maxWeight uint64) *main[K, V] {
	return &main[K, V]{
		d:         deque.NewLinked[K, V](isExp),
		maxWeight: maxWeight,
	}
}

func (m *main[K, V]) insert(n node.Node[K, V]) {
	nodeWeight := uint64(n.Weight())
	if nodeWeight > m.maxWeight {
		m.d.PushFront(n)
	} else {
		m.d.PushBack(n)
	}
	n.MarkMain()
	m.weight += uint64(n.Weight())
}

func (m *main[K, V]) evict(nowNanos int64, evictNode func(n node.Node[K, V], nowNanos int64)) {
	reinsertions := 0
	for m.weight > 0 {
		n := m.d.PopFront()
		n.Unmark()
		m.weight -= uint64(n.Weight())

		if !n.IsAlive() || n.HasExpired(nowNanos) || n.Frequency() == 0 {
			evictNode(n, nowNanos)
			return
		}

		// to avoid the worst case O(n), we remove the 20th reinserted consecutive element.
		reinsertions++
		if reinsertions >= maxReinsertions {
			evictNode(n, nowNanos)
			return
		}

		m.insert(n)
		n.DecrementFrequency()
	}
}

func (m *main[K, V]) delete(n node.Node[K, V]) {
	m.weight -= uint64(n.Weight())
	n.Unmark()
	m.d.Delete(n)
}

func (m *main[K, V]) len() int {
	return m.d.Len()
}

func (m *main[K, V]) clear() {
	m.d.Clear()
	m.weight = 0
}

func (m *main[K, V]) isFull() bool {
	return m.weight >= m.maxWeight
}