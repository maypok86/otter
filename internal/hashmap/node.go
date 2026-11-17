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

package hashmap

import "github.com/maypok86/otter/v2/internal/generated/node"

// EntryMap wraps a V-erased Map[K, node.Keyed[K]] (compiled once per K shape) with strictly
// typed *node.Entry[K, V] handles.
type EntryMap[K comparable, V any] struct {
	m  *Map[K, node.Keyed[K]]
	nm *node.Manager[K, V]
}

func NewEntryMap[K comparable, V any](nm *node.Manager[K, V]) *EntryMap[K, V] {
	return &EntryMap[K, V]{
		m:  New[K, node.Keyed[K]](nm.KeyLens().Key),
		nm: nm,
	}
}

func NewEntryMapWithSize[K comparable, V any](nm *node.Manager[K, V], size int) *EntryMap[K, V] {
	return &EntryMap[K, V]{
		m:  NewWithSize[K, node.Keyed[K]](nm.KeyLens().Key, size),
		nm: nm,
	}
}

func (em *EntryMap[K, V]) Get(key K) *node.Entry[K, V] {
	return em.nm.FromKeyed(em.m.Get(key))
}

func (em *EntryMap[K, V]) Compute(key K, fn func(n *node.Entry[K, V]) *node.Entry[K, V]) *node.Entry[K, V] {
	nm := em.nm
	return nm.FromKeyed(em.m.Compute(key, func(k *node.Keyed[K]) *node.Keyed[K] {
		return nm.ToKeyed(fn(nm.FromKeyed(k)))
	}))
}

func (em *EntryMap[K, V]) Range(fn func(n *node.Entry[K, V]) bool) {
	nm := em.nm
	em.m.Range(func(k *node.Keyed[K]) bool {
		return fn(nm.FromKeyed(k))
	})
}

func (em *EntryMap[K, V]) Size() int {
	return em.m.Size()
}
