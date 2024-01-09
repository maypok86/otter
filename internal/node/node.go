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

package node

import (
	"github.com/maypok86/otter/internal/unixtime"
)

const (
	unknownQueueType uint8 = iota
	smallQueueType
	mainQueueType

	maxFrequency uint8 = 3
)

// Node is an entry in the cache containing the key, value, cost, access and write metadata.
type Node[K comparable, V any] struct {
	key        K
	value      V
	prev       *Node[K, V]
	next       *Node[K, V]
	expiration uint32
	cost       uint32
	frequency  uint8
	queueType  uint8
}

// New creates a new Node.
func New[K comparable, V any](key K, value V, expiration, cost uint32) *Node[K, V] {
	return &Node[K, V]{
		key:        key,
		value:      value,
		expiration: expiration,
		cost:       cost,
	}
}

// Key returns the key.
func (n *Node[K, V]) Key() K {
	return n.key
}

// Value returns the value.
func (n *Node[K, V]) Value() V {
	return n.value
}

// IsExpired returns true if node is expired.
func (n *Node[K, V]) IsExpired() bool {
	return n.expiration > 0 && n.expiration < unixtime.Now()
}

// Expiration returns the expiration time.
func (n *Node[K, V]) Expiration() uint32 {
	return n.expiration
}

// Cost returns the cost of the node.
func (n *Node[K, V]) Cost() uint32 {
	return n.cost
}

// Frequency returns the frequency of the node.
func (n *Node[K, V]) Frequency() uint8 {
	return n.frequency
}

// IncrementFrequency increments the frequency of the node.
func (n *Node[K, V]) IncrementFrequency() {
	n.frequency = minUint8(n.frequency+1, maxFrequency)
}

// DecrementFrequency decrements the frequency of the node.
func (n *Node[K, V]) DecrementFrequency() {
	n.frequency--
}

// ResetFrequency resets the frequency.
func (n *Node[K, V]) ResetFrequency() {
	n.frequency = 0
}

// MarkSmall sets the status to the small queue.
func (n *Node[K, V]) MarkSmall() {
	n.queueType = smallQueueType
}

// IsSmall returns true if node is in the small queue.
func (n *Node[K, V]) IsSmall() bool {
	return n.queueType == smallQueueType
}

// MarkMain sets the status to the main queue.
func (n *Node[K, V]) MarkMain() {
	n.queueType = mainQueueType
}

// IsMain returns true if node is in the main queue.
func (n *Node[K, V]) IsMain() bool {
	return n.queueType == mainQueueType
}

// Unmark sets the status to unknown.
func (n *Node[K, V]) Unmark() {
	n.queueType = unknownQueueType
}

func minUint8(a, b uint8) uint8 {
	if a < b {
		return a
	}

	return b
}
