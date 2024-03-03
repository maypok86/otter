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

// Policy is an eviction policy based on S3-FIFO eviction algorithm
// from the following paper: https://dl.acm.org/doi/10.1145/3600006.3613147.
type Policy[K comparable, V any] struct {
	small                *small[K, V]
	main                 *main[K, V]
	ghost                *ghost[K, V]
	maxCost              uint32
	maxAvailableNodeCost uint32
}

// NewPolicy creates a new Policy.
func NewPolicy[K comparable, V any](maxCost uint32) *Policy[K, V] {
	smallMaxCost := maxCost / 10
	mainMaxCost := maxCost - smallMaxCost

	main := newMain[K, V](mainMaxCost)
	ghost := newGhost(main)
	small := newSmall(smallMaxCost, main, ghost)
	ghost.small = small

	return &Policy[K, V]{
		small:                small,
		main:                 main,
		ghost:                ghost,
		maxCost:              maxCost,
		maxAvailableNodeCost: smallMaxCost,
	}
}

// Read updates the eviction policy based on node accesses.
func (p *Policy[K, V]) Read(nodes []node.Node[K, V]) {
	for _, n := range nodes {
		n.IncrementFrequency()
	}
}

// Add adds node to the eviction policy.
func (p *Policy[K, V]) Add(deleted []node.Node[K, V], n node.Node[K, V]) []node.Node[K, V] {
	if p.ghost.isGhost(n) {
		p.main.insert(n)
		n.ResetFrequency()
	} else {
		p.small.insert(n)
	}

	for p.isFull() {
		deleted = p.evict(deleted)
	}

	return deleted
}

func (p *Policy[K, V]) evict(deleted []node.Node[K, V]) []node.Node[K, V] {
	if p.small.cost >= p.maxCost/10 {
		return p.small.evict(deleted)
	}

	return p.main.evict(deleted)
}

func (p *Policy[K, V]) isFull() bool {
	return p.small.cost+p.main.cost > p.maxCost
}

// Delete deletes node from the eviction policy.
func (p *Policy[K, V]) Delete(n node.Node[K, V]) {
	if n.IsSmall() {
		p.small.remove(n)
		return
	}

	if n.IsMain() {
		p.main.remove(n)
	}
}

// MaxAvailableCost returns the maximum available cost of the node.
func (p *Policy[K, V]) MaxAvailableCost() uint32 {
	return p.maxAvailableNodeCost
}

// Clear clears the eviction policy and returns it to the default state.
func (p *Policy[K, V]) Clear() {
	p.ghost.clear()
	p.main.clear()
	p.small.clear()
}
