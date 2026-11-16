// Copyright (c) 2023 Alexey Mayshev and contributors. All rights reserved.
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

package otter

import (
	"github.com/maypok86/otter/v2/internal/generated/node"
)

// reason represents the reason for writing the item to the cache.
type reason uint8

const (
	unknownReason reason = iota
	addReason
	deleteReason
	updateReason
	// reconcileReason reports a weight changed by an in-place update, or an expiration time
	// moved earlier by SetExpiresAfter.
	reconcileReason
)

// task is a set of information to update the cache:
// node, reason for write, difference after node weight change, etc.
type task struct {
	n           *node.Header
	old         *node.Header
	writeReason reason
	// quiet reports an update that is not an access: the completion of a refresh, whose
	// request was counted by the read that started it.
	quiet bool
}

// newTask allocates a task. Tasks are not pooled: writers on every P would take them and
// maintenance on one P would return them, so writers would mostly steal from the pool of that P,
// which costs more than the allocation.
func newTask[K comparable, V any](n, old *node.Entry[K, V], writeReason reason) *task {
	return &task{
		n:           n.Header(),
		old:         old.Header(),
		writeReason: writeReason,
	}
}
