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
	"github.com/maypok86/otter/v2/internal/generated/node"
)

type small[K comparable, V any] struct {
	q         *queue[K, V]
	main      *main[K, V]
	ghost     *ghost[K, V]
	weight    uint64
	maxWeight uint64
	evictNode func(node.Node[K, V])
}

func newSmall[K comparable, V any](
	maxWeight uint64,
	main *main[K, V],
	ghost *ghost[K, V],
	evictNode func(node.Node[K, V]),
) *small[K, V] {
	return &small[K, V]{
		q:         newQueue[K, V](),
		main:      main,
		ghost:     ghost,
		maxWeight: maxWeight,
		evictNode: evictNode,
	}
}

func (s *small[K, V]) insert(n node.Node[K, V]) {
	s.q.push(n)
	n.MarkSmall()
	s.weight += uint64(n.Weight())
}

func (s *small[K, V]) evict(nowNanos int64) {
	if s.weight == 0 {
		return
	}

	n := s.q.pop()
	s.weight -= uint64(n.Weight())
	n.Unmark()
	if !n.IsAlive() || n.HasExpired(nowNanos) {
		s.evictNode(n)
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

	s.ghost.insert(n)
}

func (s *small[K, V]) delete(n node.Node[K, V]) {
	s.weight -= uint64(n.Weight())
	n.Unmark()
	s.q.delete(n)
}

func (s *small[K, V]) length() int {
	return s.q.length()
}

func (s *small[K, V]) clear() {
	s.q.clear()
	s.weight = 0
}
