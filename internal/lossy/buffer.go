package lossy

import (
	"sync/atomic"
	"unsafe"

	"github.com/maypok86/otter/internal/xruntime"
)

const (
	capacity = 64
	mask     = uint64(capacity - 1)
)

type PolicyBuffers[T any] struct {
	Returned []T
	Deleted  []T
}

type Buffer[T any] struct {
	head                 atomic.Uint64
	headPadding          [xruntime.CacheLineSize - unsafe.Sizeof(atomic.Uint64{})]byte
	tail                 atomic.Uint64
	tailPadding          [xruntime.CacheLineSize - unsafe.Sizeof(atomic.Uint64{})]byte
	returned             unsafe.Pointer
	returnedPadding      [xruntime.CacheLineSize - 8]byte
	policyBuffers        *PolicyBuffers[T]
	returnedSlicePadding [xruntime.CacheLineSize - 8]byte
	buffer               [capacity]T
}

func New[T any]() *Buffer[T] {
	pb := &PolicyBuffers[T]{
		Returned: make([]T, 0, capacity),
		Deleted:  make([]T, 0, capacity),
	}
	b := &Buffer[T]{
		policyBuffers: pb,
	}
	b.returned = unsafe.Pointer(b.policyBuffers)
	return b
}

func (b *Buffer[T]) Add(item T) *PolicyBuffers[T] {
	head := b.head.Load()
	tail := b.tail.Load()
	size := tail - head
	if size >= capacity {
		// full buffer
		return nil
	}
	if b.tail.CompareAndSwap(tail, tail+1) {
		// success
		index := int(tail & mask)
		b.buffer[index] = item
		if size == capacity-1 {
			// try return new buffer
			if !atomic.CompareAndSwapPointer(&b.returned, unsafe.Pointer(b.policyBuffers), nil) {
				// somebody already get buffer
				return nil
			}

			pb := b.policyBuffers
			for i := 0; i < capacity; i++ {
				index := int(head & mask)
				pb.Returned = append(pb.Returned, b.buffer[index])
				head++
			}

			b.head.Add(capacity)
			return pb
		}
	}

	// failed
	return nil
}

func (b *Buffer[T]) Free() {
	b.policyBuffers.Returned = b.policyBuffers.Returned[:0]
	b.policyBuffers.Deleted = b.policyBuffers.Deleted[:0]
	atomic.StorePointer(&b.returned, unsafe.Pointer(b.policyBuffers))
}
