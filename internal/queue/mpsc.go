package queue

import (
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/maypok86/otter/internal/xruntime"
)

func zeroValue[T any]() T {
	var zero T
	return zero
}

type MPSC[T any] struct {
	capacity    uint64
	head        atomic.Uint64
	headPadding [xruntime.CacheLineSize - unsafe.Sizeof(atomic.Uint64{})]byte
	tail        uint64
	tailPadding [xruntime.CacheLineSize - 8]byte
	slots       []paddedSlot[T]
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
		capacity: uint64(capacity),
		slots:    make([]paddedSlot[T], capacity),
	}
}

func (q *MPSC[T]) Insert(item T) {
	head := q.head.Add(1) - 1
	slot := &q.slots[q.idx(head)]
	turn := q.turn(head) * 2
	for slot.turn.Load() != turn {
		runtime.Gosched()
	}
	slot.item = item
	slot.turn.Store(turn + 1)
}

func (q *MPSC[T]) Remove() T {
	tail := q.tail
	slot := &q.slots[q.idx(tail)]
	turn := 2*q.turn(tail) + 1
	for slot.turn.Load() != turn {
		runtime.Gosched()
	}
	item := slot.item
	slot.item = zeroValue[T]()
	slot.turn.Store(turn + 1)
	q.tail++
	return item
}

func (q *MPSC[T]) Capacity() int {
	return int(q.capacity)
}

func (q *MPSC[T]) idx(i uint64) uint64 {
	return i % q.capacity
}

func (q *MPSC[T]) turn(i uint64) uint64 {
	return i / q.capacity
}
