// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
// Copyright (c) 2023 Andrey Pechkurov
//
// Copyright notice. This code is a fork of xsync.MPMCQueueOf from this file with many changes:
// https://github.com/puzpuzpuz/xsync/blob/main/mpmcqueueof.go
//
// Use of this source code is governed by a MIT license that can be found
// at https://github.com/puzpuzpuz/xsync/blob/main/LICENSE

package queue

import (
	"runtime"
	"sync/atomic"

	"github.com/maypok86/otter/internal/xruntime"
)

const (
	maxRetries = 16
)

func zeroValue[T any]() T {
	var zero T
	return zero
}

// A MPSC is a bounded multi-producer single-consumer concurrent queue.
//
// MPSC instances must be created with NewMPSC function.
// A MPSC must not be copied after first use.
//
// Based on the data structure from the following C++ library:
// https://github.com/rigtorp/MPMCQueue
type MPSC[T any] struct {
	capacity     uint64
	sleep        chan struct{}
	head         uint64
	headPadding  [xruntime.CacheLineSize - 8]byte
	tail         uint64
	tailPadding  [xruntime.CacheLineSize - 8]byte
	isSleep      uint64
	sleepPadding [xruntime.CacheLineSize - 8]byte
	slots        []slot[T]
}

type slot[T any] struct {
	// uint64 is used here to get proper 8 byte alignment on 32-bit archs.
	turn uint64
	item T
}

// NewMPSC creates a new MPSC instance with the given capacity.
func NewMPSC[T any](capacity int) *MPSC[T] {
	return &MPSC[T]{
		sleep:    make(chan struct{}),
		capacity: uint64(capacity),
		slots:    make([]slot[T], capacity),
	}
}

// Insert inserts the given item into the queue.
// Blocks, if the queue is full.
func (q *MPSC[T]) Insert(item T) {
	head := atomic.AddUint64(&q.head, 1) - 1
	q.wakeUpConsumer()

	slot := &q.slots[q.idx(head)]
	turn := q.turn(head) * 2
	retries := 0
	for atomic.LoadUint64(&slot.turn) != turn {
		if retries == maxRetries {
			q.wakeUpConsumer()
			retries = 0
			continue
		}
		retries++
		runtime.Gosched()
	}

	slot.item = item
	atomic.StoreUint64(&slot.turn, turn+1)
}

// Remove retrieves and removes the item from the head of the queue.
// Blocks, if the queue is empty.
func (q *MPSC[T]) Remove() T {
	tail := q.tail
	slot := &q.slots[q.idx(tail)]
	turn := 2*q.turn(tail) + 1
	retries := 0
	for atomic.LoadUint64(&slot.turn) != turn {
		if retries == maxRetries {
			q.sleepConsumer()
			retries = 0
			continue
		}
		retries++
		runtime.Gosched()
	}
	item := slot.item
	slot.item = zeroValue[T]()
	atomic.StoreUint64(&slot.turn, turn+1)
	q.tail++
	return item
}

// Clear clears the queue.
func (q *MPSC[T]) Clear() {
	for !q.isEmpty() {
		_ = q.Remove()
	}
}

// Capacity returns capacity of the queue.
func (q *MPSC[T]) Capacity() int {
	return int(q.capacity)
}

func (q *MPSC[T]) wakeUpConsumer() {
	if atomic.LoadUint64(&q.isSleep) == 1 && atomic.CompareAndSwapUint64(&q.isSleep, 1, 0) {
		// if the consumer is asleep, we'll wake him up.
		q.sleep <- struct{}{}
	}
}

func (q *MPSC[T]) sleepConsumer() {
	// if the queue's been empty for too long, we fall asleep.
	atomic.StoreUint64(&q.isSleep, 1)
	<-q.sleep
}

func (q *MPSC[T]) isEmpty() bool {
	return q.tail == atomic.LoadUint64(&q.head)
}

func (q *MPSC[T]) idx(i uint64) uint64 {
	return i % q.capacity
}

func (q *MPSC[T]) turn(i uint64) uint64 {
	return i / q.capacity
}
