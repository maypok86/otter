package queue

import (
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/maypok86/otter/internal/xruntime"
)

const (
	maxRetries = 16
)

func zeroValue[T any]() T {
	var zero T
	return zero
}

type MPSC[T any] struct {
	capacity     uint64
	sleep        chan struct{}
	head         atomic.Uint64
	headPadding  [xruntime.CacheLineSize - unsafe.Sizeof(atomic.Uint64{})]byte
	tail         uint64
	tailPadding  [xruntime.CacheLineSize - 8]byte
	isSleep      atomic.Uint64
	sleepPadding [xruntime.CacheLineSize - unsafe.Sizeof(atomic.Uint64{})]byte
	slots        []paddedSlot[T]
}

type paddedSlot[T any] struct {
	slot[T]
	// TODO: padding
	padding [xruntime.CacheLineSize - unsafe.Sizeof(atomic.Uint64{}) - 8]byte
}

type slot[T any] struct {
	turn atomic.Uint64
	item T
}

func NewMPSC[T any](capacity int) *MPSC[T] {
	return &MPSC[T]{
		sleep:    make(chan struct{}),
		capacity: uint64(capacity),
		slots:    make([]paddedSlot[T], capacity),
	}
}

func (q *MPSC[T]) Insert(item T) {
	head := q.head.Add(1) - 1
	q.wakeUpConsumer()

	slot := &q.slots[q.idx(head)]
	turn := q.turn(head) * 2
	retries := 0
	for slot.turn.Load() != turn {
		if retries == maxRetries {
			q.wakeUpConsumer()
			retries = 0
			continue
		}
		retries++
		runtime.Gosched()
	}

	slot.item = item
	slot.turn.Store(turn + 1)
}

func (q *MPSC[T]) Remove() T {
	tail := q.tail
	slot := &q.slots[q.idx(tail)]
	turn := 2*q.turn(tail) + 1
	retries := 0
	for slot.turn.Load() != turn {
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
	slot.turn.Store(turn + 1)
	q.tail++
	return item
}

func (q *MPSC[T]) Clear() {
	for !q.isEmpty() {
		_ = q.Remove()
	}
}

func (q *MPSC[T]) Capacity() int {
	return int(q.capacity)
}

func (q *MPSC[T]) wakeUpConsumer() {
	if q.isSleep.CompareAndSwap(1, 0) {
		// if the consumer is asleep, we'll wake him up.
		q.sleep <- struct{}{}
	}
}

func (q *MPSC[T]) sleepConsumer() {
	// if the queue's been empty for too long, we fall asleep.
	q.isSleep.Store(1)
	<-q.sleep
}

func (q *MPSC[T]) isEmpty() bool {
	return q.tail == q.head.Load()
}

func (q *MPSC[T]) idx(i uint64) uint64 {
	return i % q.capacity
}

func (q *MPSC[T]) turn(i uint64) uint64 {
	return i / q.capacity
}
