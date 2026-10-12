// Copyright (c) 2025 Alexey Mayshev and contributors. All rights reserved.
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

// A key mutation changes what a key holds, deciding from what it holds now: Set, SetIfAbsent,
// the Compute family, Invalidate and the completion of a load or refresh are all one. mutate
// decides and applies the change under the key's bucket lock, and publish hands it to the
// policies and the OnDeletion listener once the lock is released. In between, the mutation
// carries everything decided under the lock, so that the work after it agrees with it: an
// expired entry is absent for every mutation, and both deletion listeners get the same cause.
//
// Removing a given node (eviction, expiration, InvalidateAll) is not a key mutation: it does
// not decide from the key's value, and its work after the lock belongs to maintenance.

// origin is where a key mutation comes from. It decides which pending loads of the key the
// mutation cancels, whether a write is a reload for the refresh calculator, and how the
// OnDeletion listener is called.
type origin uint8

const (
	// fromCaller is a call of the cache's API. A write or an invalidation cancels the key's
	// pending loads and refreshes, so that they cannot bring back the value it replaced.
	fromCaller origin = iota
	// fromLoad is the completion of a load. It cancels nothing: the load's own call is deleted
	// after the mutation is published.
	fromLoad
	// fromRefresh is the completion of a refresh, which runs as an executor task. It cancels
	// nothing either, and it calls the OnDeletion listener directly instead of submitting it to
	// the executor (see notifyDeletionFrom).
	fromRefresh
)

// mutationOp is what a key mutation does, as decided from the key's current entry.
type mutationOp uint8

const (
	// opKeep leaves the key as it is.
	opKeep mutationOp = iota
	// opWrite writes a value, over the current entry if there is one.
	opWrite
	// opInvalidate removes the current entry, if there is one.
	opInvalidate
	// opRemoveExpired removes the current entry, which has expired, as the timer wheel would:
	// a pending load of the key is kept, since it most likely started because the entry
	// expired, and only a refresh of the expired value is dropped.
	opRemoveExpired
)

// previous is the key's entry when a mutation is decided.
type previous[K comparable, V any] struct {
	// node is the key's node, or nil if the key is absent.
	node node.Node[K, V]
	// value is node's value, read under the lock.
	value V
	// live reports that node holds a value that has not expired.
	live bool
}

// mutation is a key mutation as decided and applied under the bucket lock.
type mutation[K comparable, V any] struct {
	previous[K, V]
	// op is what the mutation did.
	op mutationOp
	// from is where the mutation came from.
	from origin
	// nowNano is the time the mutation was decided at.
	nowNano int64
	// next is the key's node afterwards, or nil if it is absent. After an update in place it is
	// previous.node.
	next node.Node[K, V]
	// writeResult is what the write or removal decided about the previous value and, for a
	// write, the new one.
	writeResult
}

// mutate changes what key holds as decide says, under the key's bucket lock. decide is given
// the key's current entry and returns the op and, for opWrite, the value; it runs under the
// lock, may change the deadlines of the entry it was given, and returns opRemoveExpired only
// for an expired entry. The caller passes the result to publish once mutate returns.
func (c *cache[K, V]) mutate(
	key K,
	nowNano int64,
	from origin,
	decide func(prev previous[K, V]) (mutationOp, V),
) mutation[K, V] {
	m := mutation[K, V]{from: from, nowNano: nowNano}
	m.next = c.hashmap.Compute(key, func(current node.Node[K, V]) node.Node[K, V] {
		m.node = current
		if current != nil {
			m.value = current.Value()
			m.live = !current.HasExpired(nowNano)
		}

		var value V
		m.op, value = decide(m.previous)
		switch m.op {
		case opWrite:
			var n node.Node[K, V]
			n, m.writeResult = c.atomicSet(key, value, current, from, nowNano)
			return n
		case opInvalidate:
			if from == fromCaller {
				c.singleflight.invalidate(key)
			}
			m.cause = c.retireRemoved(current, nowNano)
			return nil
		case opRemoveExpired:
			c.singleflight.deleteOnRemoval(key)
			m.cause = c.retireRemoved(current, nowNano)
			return nil
		default:
			return current
		}
	})
	// A write or an invalidation cancels the key's calls inside the callback, before the hash
	// table stores the new node or removes the old one, so a call registered in between can still
	// find the old value: its owner re-checks the table and adopts it, and a Get that starts after
	// this returns would join it. Cancelling again once the change is visible ends such calls.
	if from == fromCaller && (m.op == opWrite || m.op == opInvalidate) {
		c.singleflight.delete(key)
	}
	return m
}

// publish hands a mutation to the policies, through the write or read buffer, and reports the
// previous value to the OnDeletion listener with the cause decided under the lock.
func (c *cache[K, V]) publish(m mutation[K, V]) {
	onExecutor := m.from == fromRefresh
	switch m.op {
	case opWrite:
		c.afterWrite(m, onExecutor)
	case opInvalidate, opRemoveExpired:
		c.afterDelete(m.node, m.cause, false, onExecutor)
	default:
	}
}

// afterWrite publishes a write. It takes the previous value from the mutation, because after an
// in-place update the previous node is the written one.
func (c *cache[K, V]) afterWrite(m mutation[K, V], onExecutor bool) {
	n, old := m.next, m.node
	if !c.withMaintenance {
		if old != nil {
			c.notifyDeletionFrom(onExecutor, old.Key(), m.value, m.cause)
		}
		return
	}

	if m.inPlace {
		// Updated in place: the policies observe an access through the lossy read buffer, and a
		// changed weight or an earlier expiration time through a task, which must not be lost.
		c.afterRead(n, m.nowNano, false, false)
		if m.reconcile {
			c.afterWriteTask(newTask(n, nil, reconcileReason, causeUnknown))
		}
		c.notifyDeletionFrom(onExecutor, n.Key(), m.value, m.cause)
		return
	}

	if old == nil {
		// insert
		c.afterWriteTask(newTask(n, nil, addReason, causeUnknown))
		return
	}

	// update
	c.afterWriteTask(newTask(n, old, updateReason, m.cause))
}

// atomicSet applies a write of value to the key whose current node is old (nil if absent).
// The user's weigher, expiry and refresh calculators are called once per write.
//
// A write over a node that can set its value and is still in the table is applied in place,
// keeping the node:
// plain updates, loads and refreshes, and writes over an expired entry that has not been
// removed yet (it is reused for the new value, and the old one is reported as expired).
// Swapping in a new node forces an update task through the bounded write buffer so that the
// policies can relink the node. Under an update-heavy workload that buffer overflows and every
// writer falls back to performCleanUp, serializing all writes on evictionMutex. When the node
// identity is kept, the policies only need to observe an access, which goes through the lossy
// read buffer, unless the weight changed or the expiration time moved earlier: the eviction
// policy accounts the weight separately (policyWeight) and the timer wheel only reschedules a
// deadline that moved later, so those changes go through a reconciliation task.
//
// A value that fits in one atomic word (a pointer-shaped value, or one without pointers of up to
// 8 bytes) is always set in place. A larger value is kept inline, and an inline value is never
// written after publication, so that a concurrent reader cannot observe a torn or cleared value:
// the first update of such a live entry replaces the node with a boxed one, whose value lives
// behind an atomic pointer, so that later updates of this (likely hot) key are applied in place.
func (c *cache[K, V]) atomicSet(
	key K,
	value V,
	old node.Node[K, V],
	from origin,
	nowNano int64,
) (node.Node[K, V], writeResult) {
	// The user's weigher and calculators run before anything is changed: if one of them panics,
	// the write is not applied and the panic reaches the caller.
	var oldValue V
	if old != nil {
		oldValue = old.Value()
	}
	weight := c.weigher(key, value)
	kind := newWriteKind(old != nil && !old.HasExpired(nowNano), from == fromRefresh)
	base := old
	if kind == createWrite {
		base = nil
	}
	entry := c.writeEntry(key, value, weight, base, nowNano)
	if c.withExpiration {
		entry.ExpiresAtNano = expiresAtAfterWrite(c.expiryCalculator, kind, entry, oldValue, nowNano)
	}
	if c.withRefresh {
		// after the expiration time, so that the refresh calculator sees the new one
		entry.RefreshableAtNano = refreshableAtAfterWrite(c.refreshCalculator, kind, entry, oldValue, nil, nowNano)
	}

	if old != nil && c.withMaintenance && old.IsAlive() && old.CanSetValue() {
		written := writeResult{
			// before the node is changed: an expired entry is reused, and its old value expired
			cause:   getCause(old, nowNano, CauseReplacement),
			inPlace: true,
			reconcile: weight != old.Weight() ||
				(c.withExpiration && entry.ExpiresAtNano < old.ExpiresAt()),
			expiresAt: entry.ExpiresAtNano,
		}
		// The synchronous listener runs before the new value is published, as it does when the
		// node is replaced: readers see the previous value until the write completes.
		c.notifyAtomicDeletion(key, oldValue, written.cause)
		old.SetValue(value)
		if c.isWeighted {
			// only weighted nodes store a weight; the others always weigh 1
			old.SetWeight(weight)
		}
		if c.withExpiration {
			old.SetExpiresAt(entry.ExpiresAtNano)
		}
		if c.withRefresh {
			old.SetRefreshableAt(entry.RefreshableAtNano)
		}
		c.cancelCalls(key, from)
		return old, written
	}

	n := c.newNode(key, value, weight, entry.ExpiresAtNano, entry.RefreshableAtNano, c.isLiveNode(old, nowNano))
	c.makeRetired(old)
	written := writeResult{expiresAt: entry.ExpiresAtNano}
	if old != nil {
		written.cause = getCause(old, nowNano, CauseReplacement)
		c.notifyAtomicDeletion(old.Key(), oldValue, written.cause)
	}
	c.cancelCalls(key, from)
	return n, written
}

// cancelCalls cancels the pending load or refresh of key when a write from the cache's API
// changes it. It runs after the new value is published, including the OnAtomicDeletion call
// before that: a refresh requested from the previous value while the write was in progress is
// cancelled too. A load or refresh requested after this point either sees the new value or, if
// the node is replaced, finds that its base node is gone.
func (c *cache[K, V]) cancelCalls(key K, from origin) {
	if from == fromCaller {
		c.singleflight.delete(key)
	}
}

// writeResult is what a write or a removal decided under the bucket lock about the previous
// value and, for a write, the new one, so that the work done after the lock is released agrees
// with it.
type writeResult struct {
	// cause is why the previous value left the cache: CauseReplacement or CauseInvalidation, or
	// CauseExpiration when it had expired. It is reported to both deletion listeners; recomputing it later could
	// disagree, because the deadline of a replaced node can still be changed by a reader that
	// holds it. Unset when there was no previous node.
	cause DeletionCause
	// inPlace reports that the existing node was updated instead of being replaced.
	inPlace bool
	// reconcile reports a changed weight or an earlier expiration time of an in-place update,
	// which the policies cannot pick up from an access.
	reconcile bool
	// expiresAt is the expiration time a write gave the new value. Once the lock is released,
	// another write can update the node again, so it is not read back from the node.
	expiresAt int64
}

// isLiveNode reports whether n is in the hash table and has not expired.
func (c *cache[K, V]) isLiveNode(n node.Node[K, V], nowNano int64) bool {
	return n != nil && c.withMaintenance && n.IsAlive() && !n.HasExpired(nowNano)
}

// retireRemoved retires old, which the caller removes from the hash table, without touching the
// key's pending calls, and returns the cause reported to the synchronous listener.
func (c *cache[K, V]) retireRemoved(old node.Node[K, V], nowNano int64) DeletionCause {
	if old == nil {
		return 0
	}
	cause := getCause(old, nowNano, CauseInvalidation)
	c.makeRetired(old)
	c.notifyAtomicDeletion(old.Key(), old.Value(), cause)
	return cause
}
