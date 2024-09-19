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

type small[K comparable, V any] struct {
	d         *deque.Linked[K, V]
	main      *main[K, V]
	ghost     *ghost[K, V]
	weight    uint64
	maxWeight uint64
	evictNode func(n node.Node[K, V], nowNanos int64)
}

func newSmall[K comparable, V any](
	maxWeight uint64,
	main *main[K, V],
	ghost *ghost[K, V],
	evictNode func(n node.Node[K, V], nowNanos int64),
) *small[K, V] {
	return &small[K, V]{
		d:         deque.NewLinked[K, V](isExp),
		main:      main,
		ghost:     ghost,
		maxWeight: maxWeight,
		evictNode: evictNode,
	}
}

func (s *small[K, V]) insert(n node.Node[K, V]) {
	s.d.PushBack(n)
	n.MarkSmall()
	s.weight += uint64(n.Weight())
}

func (s *small[K, V]) evict(nowNanos int64) {
	if s.weight == 0 {
		return
	}

	n := s.d.PopFront()
	s.weight -= uint64(n.Weight())
	n.Unmark()
	if !n.IsAlive() || n.HasExpired(nowNanos) {
		s.evictNode(n, nowNanos)
		return
	}

	if n.Frequency() > 1 {
		s.main.insert(n)
		for s.main.isFull() {
			s.main.evict(nowNanos)
		}
		n.ResetFrequency()
		return
	}

	s.evictNode(n, nowNanos)
	s.ghost.insert(n)
}

func (s *small[K, V]) delete(n node.Node[K, V]) {
	s.weight -= uint64(n.Weight())
	n.Unmark()
	s.d.Delete(n)
}

func (s *small[K, V]) len() int {
	return s.d.Len()
}

func (s *small[K, V]) clear() {
	s.d.Clear()
	s.weight = 0
}
