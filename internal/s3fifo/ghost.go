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
	"github.com/dolthub/maphash"
	"github.com/gammazero/deque"

	"github.com/maypok86/otter/internal/generated/node"
)

type ghost[K comparable, V any] struct {
	q      *deque.Deque[uint64]
	m      map[uint64]struct{}
	main   *main[K, V]
	small  *small[K, V]
	hasher maphash.Hasher[K]
}

func newGhost[K comparable, V any](main *main[K, V]) *ghost[K, V] {
	return &ghost[K, V]{
		q:      deque.New[uint64](),
		m:      make(map[uint64]struct{}),
		main:   main,
		hasher: maphash.NewHasher[K](),
	}
}

func (g *ghost[K, V]) isGhost(n node.Node[K, V]) bool {
	h := g.hasher.Hash(n.Key())
	_, ok := g.m[h]
	return ok
}

func (g *ghost[K, V]) insert(deleted []node.Node[K, V], n node.Node[K, V]) []node.Node[K, V] {
	deleted = append(deleted, n)

	h := g.hasher.Hash(n.Key())

	if _, ok := g.m[h]; ok {
		return deleted
	}

	maxLength := g.small.length() + g.main.length()
	if maxLength == 0 {
		return deleted
	}

	for g.q.Len() >= maxLength {
		v := g.q.PopFront()
		delete(g.m, v)
	}

	g.q.PushBack(h)
	g.m[h] = struct{}{}

	return deleted
}

func (g *ghost[K, V]) clear() {
	g.q.Clear()
	for k := range g.m {
		delete(g.m, k)
	}
}
