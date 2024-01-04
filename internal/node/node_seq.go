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

//go:build !race

package node

import (
	"runtime"
	"sync/atomic"
)

// seqlock is used to synchronize the node and atomically get the value.
// https://en.wikipedia.org/wiki/Seqlock

// Value returns the value.
func (n *Node[K, V]) Value() V {
	// Since go compiler rearranges value copying (apparently LoadLoad barriers are not supported),
	// we have to artificially construct a LoadStore barrier using this variable.
	// https://golang.design/gossa?id=fceddb7f-ac78-11ee-ac09-0242ac16000d.
	var barrier atomic.Uint32
	for {
		seq := n.lock.Load()
		if seq&1 != 0 {
			runtime.Gosched()
			continue
		}

		value := n.value

		// Explicitly forbid the compiler to move the value copying.
		barrier.Store(seq)

		newSeq := n.lock.Load()
		if seq == newSeq {
			return value
		}
	}
}

// Lock locks the node for updates.
func (n *Node[K, V]) Lock() {
	for {
		seq := n.lock.Load()
		if seq&1 != 0 {
			runtime.Gosched()
			continue
		}

		if n.lock.CompareAndSwap(seq, seq+1) {
			return
		}
	}
}

// Unlock unlocks the node.
func (n *Node[K, V]) Unlock() {
	n.lock.Add(1)
}
