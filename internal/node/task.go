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

// reason represents the reason for writing the item to the cache.
type reason uint8

const (
	addReason reason = iota + 1
	deleteReason
	updateReason
	clearReason
	closeReason
)

// WriteTask is a set of information to update the cache:
// node, reason for write, difference after node cost change, etc.
type WriteTask[K comparable, V any] struct {
	n           *Node[K, V]
	oldNode     *Node[K, V]
	writeReason reason
}

// NewAddTask creates a task to add a node to policies.
func NewAddTask[K comparable, V any](n *Node[K, V]) WriteTask[K, V] {
	return WriteTask[K, V]{
		n:           n,
		writeReason: addReason,
	}
}

// NewDeleteTask creates a task to delete a node from policies.
func NewDeleteTask[K comparable, V any](n *Node[K, V]) WriteTask[K, V] {
	return WriteTask[K, V]{
		n:           n,
		writeReason: deleteReason,
	}
}

// NewUpdateTask creates a task to update the node in the policies.
func NewUpdateTask[K comparable, V any](n, oldNode *Node[K, V]) WriteTask[K, V] {
	return WriteTask[K, V]{
		n:           n,
		oldNode:     oldNode,
		writeReason: updateReason,
	}
}

// NewClearTask creates a task to clear policies.
func NewClearTask[K comparable, V any]() WriteTask[K, V] {
	return WriteTask[K, V]{
		writeReason: clearReason,
	}
}

// NewCloseTask creates a task to clear policies and stop all goroutines.
func NewCloseTask[K comparable, V any]() WriteTask[K, V] {
	return WriteTask[K, V]{
		writeReason: closeReason,
	}
}

// Node returns the node contained in the task. If node was not specified, it returns nil.
func (t *WriteTask[K, V]) Node() *Node[K, V] {
	return t.n
}

// OldNode returns the old node contained in the task. If old node was not specified, it returns nil.
func (t *WriteTask[K, V]) OldNode() *Node[K, V] {
	return t.oldNode
}

// IsAdd returns true if this is an add task.
func (t *WriteTask[K, V]) IsAdd() bool {
	return t.writeReason == addReason
}

// IsDelete returns true if this is a delete task.
func (t *WriteTask[K, V]) IsDelete() bool {
	return t.writeReason == deleteReason
}

// IsUpdate returns true if this is an update task.
func (t *WriteTask[K, V]) IsUpdate() bool {
	return t.writeReason == updateReason
}

// IsClear returns true if this is a clear task.
func (t *WriteTask[K, V]) IsClear() bool {
	return t.writeReason == clearReason
}

// IsClose returns true if this is a close task.
func (t *WriteTask[K, V]) IsClose() bool {
	return t.writeReason == closeReason
}
