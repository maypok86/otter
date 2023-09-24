package s3fifo

import (
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/maypok86/otter/internal/xruntime"
)

type queue[T any] struct {
	capacity    uint64
	head        atomic.Uint64
	headPadding [xruntime.CacheLineSize - unsafe.Sizeof(atomic.Uint64{})]byte
	tail        atomic.Uint64
	tailPadding [xruntime.CacheLineSize - unsafe.Sizeof(atomic.Uint64{})]byte
	slots       []paddedSlot[T]
}

type paddedSlot[T any] struct {
	slot[T]
	padding [xruntime.CacheLineSize - unsafe.Sizeof(atomic.Uint64{})]byte
}

type slot[I any] struct {
	turn atomic.Uint64
	item I
}

func newQueue[T any](capacity int) *queue[T] {
	if capacity < 1 {
		panic("capacity must be positive number")
	}
	return &queue[T]{
		capacity: uint64(capacity),
		slots:    make([]paddedSlot[T], capacity),
	}
}

func (q *queue[T]) tryInsert(item T) bool {
	head := q.head.Load()
	for {
		slot := &q.slots[q.idx(head)]
		turn := 2 * q.turn(head)
		if slot.turn.Load() == turn {
			if q.head.CompareAndSwap(head, head+1) {
				slot.item = item
				slot.turn.Store(turn + 1)
				return true
			}
		} else {
			previousHead := head
			head = q.head.Load()
			if head == previousHead {
				return false
			}
		}
		runtime.Gosched()
	}
}

func (q *queue[T]) tryRemove() (T, bool) {
	tail := q.tail.Load()
	for {
		slot := &q.slots[q.idx(tail)]
		turn := 2*q.turn(tail) + 1
		if slot.turn.Load() == turn {
			if q.tail.CompareAndSwap(tail, tail+1) {
				var zero T
				item := slot.item
				slot.item = zero
				slot.turn.Store(turn + 1)
				return item, true
			}
		} else {
			previousTail := tail
			tail = q.tail.Load()
			if tail == previousTail {
				var zero T
				return zero, false
			}
		}
		runtime.Gosched()
	}
}

func (q *queue[T]) length() int {
	return int(q.head.Load() - q.tail.Load())
}

func (q *queue[T]) idx(i uint64) uint64 {
	return i % q.capacity
}

func (q *queue[T]) turn(i uint64) uint64 {
	return i / q.capacity
}
