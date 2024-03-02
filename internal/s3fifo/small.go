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
	"github.com/maypok86/otter/internal/generated/node"
)

type small[K comparable, V any] struct {
	q       *queue[K, V]
	main    *main[K, V]
	ghost   *ghost[K, V]
	cost    uint32
	maxCost uint32
}

func newSmall[K comparable, V any](
	maxCost uint32,
	main *main[K, V],
	ghost *ghost[K, V],
) *small[K, V] {
	return &small[K, V]{
		q:       newQueue[K, V](),
		main:    main,
		ghost:   ghost,
		maxCost: maxCost,
	}
}

func (s *small[K, V]) insert(n node.Node[K, V]) {
	s.q.push(n)
	n.MarkSmall()
	s.cost += n.Cost()
}

func (s *small[K, V]) evict(deleted []node.Node[K, V]) []node.Node[K, V] {
	if s.cost == 0 {
		return deleted
	}

	n := s.q.pop()
	s.cost -= n.Cost()
	n.Unmark()
	if !n.IsAlive() || n.IsExpired() {
		return append(deleted, n)
	}

	if n.Frequency() > 1 {
		s.main.insert(n)
		for s.main.isFull() {
			deleted = s.main.evict(deleted)
		}
		n.ResetFrequency()
		return deleted
	}

	return s.ghost.insert(deleted, n)
}

func (s *small[K, V]) remove(n node.Node[K, V]) {
	s.cost -= n.Cost()
	n.Unmark()
	s.q.remove(n)
}

func (s *small[K, V]) length() int {
	return s.q.length()
}

func (s *small[K, V]) clear() {
	s.q.clear()
	s.cost = 0
}
