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

const (
	isExp        = false
	pinnedWeight = 0
)

// Policy is an eviction policy based on S3-FIFO eviction algorithm
// from the following paper: https://dl.acm.org/doi/10.1145/3600006.3613147.
type Policy[K comparable, V any] struct {
	small     *small[K, V]
	main      *main[K, V]
	ghost     *ghost[K, V]
	maxWeight uint64
}

// NewPolicy creates a new Policy.
func NewPolicy[K comparable, V any](maxWeight uint64) *Policy[K, V] {
	smallMaxWeight := maxWeight / 10
	mainMaxWeight := maxWeight - smallMaxWeight

	main := newMain[K, V](mainMaxWeight)
	ghost := newGhost(main)
	small := newSmall(smallMaxWeight, main, ghost)
	ghost.small = small

	return &Policy[K, V]{
		small:     small,
		main:      main,
		ghost:     ghost,
		maxWeight: maxWeight,
	}
}

// Read updates the eviction policy based on node accesses.
func (p *Policy[K, V]) Read(n node.Node[K, V]) {
	n.IncrementFrequency()
}

// Add adds node to the eviction policy.
func (p *Policy[K, V]) Add(n node.Node[K, V], nowNanos int64, evictNode func(n node.Node[K, V], nowNanos int64)) {
	if n.Weight() == pinnedWeight {
		return
	}

	if uint64(n.Weight()) > p.maxWeight {
		evictNode(n, nowNanos)
		return
	}

	if p.ghost.isGhost(n) {
		p.main.insert(n)
		n.ResetFrequency()
	} else {
		p.small.insert(n)
	}

	for p.isFull() {
		p.evict(nowNanos, evictNode)
	}
}

func (p *Policy[K, V]) evict(nowNanos int64, evictNode func(n node.Node[K, V], nowNanos int64)) {
	if p.small.weight >= p.maxWeight/10 {
		p.small.evict(nowNanos, evictNode)
		return
	}

	p.main.evict(nowNanos, evictNode)
}

func (p *Policy[K, V]) isFull() bool {
	return p.small.weight+p.main.weight > p.maxWeight
}

// Delete deletes node from the eviction policy.
func (p *Policy[K, V]) Delete(n node.Node[K, V]) {
	if n.IsSmall() {
		p.small.delete(n)
		return
	}

	if n.IsMain() {
		p.main.delete(n)
	}
}

// Clear clears the eviction policy and returns it to the default state.
func (p *Policy[K, V]) Clear() {
	p.ghost.clear()
	p.main.clear()
	p.small.clear()
}