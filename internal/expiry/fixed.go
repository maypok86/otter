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

package expiry

import "github.com/maypok86/otter/v2/internal/generated/node"

type Fixed[K comparable, V any] struct {
	q          *queue[K, V]
	expireNode func(n node.Node[K, V], nowNanos int64)
}

func NewFixed[K comparable, V any](expireNode func(n node.Node[K, V], nowNanos int64)) *Fixed[K, V] {
	return &Fixed[K, V]{
		q:          newQueue[K, V](),
		expireNode: expireNode,
	}
}

func (f *Fixed[K, V]) Add(n node.Node[K, V]) {
	f.q.push(n)
}

func (f *Fixed[K, V]) Delete(n node.Node[K, V]) {
	f.q.delete(n)
}

func (f *Fixed[K, V]) DeleteExpired(nowNanos int64) {
	for !f.q.isEmpty() && f.q.head.HasExpired(nowNanos) {
		f.expireNode(f.q.pop(), nowNanos)
	}
}

func (f *Fixed[K, V]) Clear() {
	f.q.clear()
}
