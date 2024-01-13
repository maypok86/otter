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

package lossy

import (
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/maypok86/otter/internal/xruntime"
)

const (
	// The maximum number of elements per buffer.
	capacity = 16
	mask     = uint64(capacity - 1)
)

// PolicyBuffers is the set of buffers returned by the lossy buffer.
type PolicyBuffers[T any] struct {
	Returned []*T
}

// Buffer is a circular ring buffer stores the elements being transferred by the producers to the consumer.
// The monotonically increasing count of reads and writes allow indexing sequentially to the next
// element location based upon a power-of-two sizing.
//
// The producers race to read the counts, check if there is available capacity, and if so then try
// once to CAS to the next write count. If the increment is successful then the producer lazily
// publishes the element. The producer does not retry or block when unsuccessful due to a failed
// CAS or the buffer being full.
//
// The consumer reads the counts and takes the available elements. The clearing of the elements
// and the next read count are lazily set.
//
// This implementation is striped to further increase concurrency.
type Buffer[T any] struct {
	head                 atomic.Uint64
	headPadding          [xruntime.CacheLineSize - unsafe.Sizeof(atomic.Uint64{})]byte
	tail                 atomic.Uint64
	tailPadding          [xruntime.CacheLineSize - unsafe.Sizeof(atomic.Uint64{})]byte
	returned             unsafe.Pointer
	returnedPadding      [xruntime.CacheLineSize - 8]byte
	policyBuffers        unsafe.Pointer
	returnedSlicePadding [xruntime.CacheLineSize - 8]byte
	buffer               [capacity]unsafe.Pointer
}

// New creates a new lossy Buffer.
func New[T any]() *Buffer[T] {
	pb := &PolicyBuffers[T]{
		Returned: make([]*T, 0, capacity),
	}
	b := &Buffer[T]{
		policyBuffers: unsafe.Pointer(pb),
	}
	b.returned = b.policyBuffers
	return b
}

// Add lazily publishes the item to the consumer.
//
// item may be lost due to contention.
func (b *Buffer[T]) Add(item *T) *PolicyBuffers[T] {
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
		atomic.StorePointer(&b.buffer[index], unsafe.Pointer(item))
		if size == capacity-1 {
			// try return new buffer
			if !atomic.CompareAndSwapPointer(&b.returned, b.policyBuffers, nil) {
				// somebody already get buffer
				return nil
			}

			pb := (*PolicyBuffers[T])(b.policyBuffers)
			for i := 0; i < capacity; i++ {
				index := int(head & mask)
				v := (*T)(atomic.LoadPointer(&b.buffer[index]))
				if v != nil {
					// published
					pb.Returned = append(pb.Returned, v)
					// release
					atomic.StorePointer(&b.buffer[index], nil)
				}
				head++
			}

			b.head.Store(head)
			return pb
		}
	}

	// failed
	return nil
}

// Free returns the processed buffer back and also clears it.
func (b *Buffer[T]) Free() {
	pb := (*PolicyBuffers[T])(b.policyBuffers)
	for i := 0; i < len(pb.Returned); i++ {
		pb.Returned[i] = nil
	}
	pb.Returned = pb.Returned[:0]
	atomic.StorePointer(&b.returned, b.policyBuffers)
}

// Clear clears the lossy Buffer and returns it to the default state.
func (b *Buffer[T]) Clear() {
	for !atomic.CompareAndSwapPointer(&b.returned, b.policyBuffers, nil) {
		runtime.Gosched()
	}
	for i := 0; i < capacity; i++ {
		atomic.StorePointer(&b.buffer[i], nil)
	}
	b.Free()
	b.tail.Store(0)
	b.head.Store(0)
}
