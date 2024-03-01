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

package expire

import "github.com/maypok86/otter/internal/generated/node"

type Fixed[K comparable, V any] struct {
	q *queue[K, V]
}

func NewFixed[K comparable, V any]() *Fixed[K, V] {
	return &Fixed[K, V]{
		q: newQueue[K, V](),
	}
}

func (f *Fixed[K, V]) Add(n node.Node[K, V]) {
	f.q.push(n)
}

func (f *Fixed[K, V]) Delete(n node.Node[K, V]) {
	f.q.remove(n)
}

func (f *Fixed[K, V]) RemoveExpired(expired []node.Node[K, V]) []node.Node[K, V] {
	for !f.q.isEmpty() && f.q.head.IsExpired() {
		expired = append(expired, f.q.pop())
	}
	return expired
}

func (f *Fixed[K, V]) Clear() {
	f.q.clear()
}
