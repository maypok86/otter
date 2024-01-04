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

//go:build race

package node

import (
	"runtime"
)

// spinlock is used to synchronize the node and atomically get the value.

const maxSpins = 16

// Value returns the value.
func (n *Node[K, V]) Value() V {
	n.Lock()
	value := n.value
	n.Unlock()
	return value
}

// Lock locks the node for updates.
func (n *Node[K, V]) Lock() {
	spins := 0
	for {
		for n.lock.Load() == 1 {
			spins++
			if spins > maxSpins {
				spins = 0
				runtime.Gosched()
			}
		}

		if n.lock.CompareAndSwap(0, 1) {
			return
		}

		spins = 0
	}
}

// Unlock unlocks the node.
func (n *Node[K, V]) Unlock() {
	n.lock.Store(0)
}
