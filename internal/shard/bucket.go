package shard

import (
	"sync"
	"unsafe"

	"github.com/maypok86/otter/internal/xruntime"
)

// <= 2 cache lines.
type paddedBucket struct {
	padding [xruntime.CacheLineSize - unsafe.Sizeof(bucket{})]byte

	bucket
}

type bucket struct {
	hashes [bucketSize]uint64
	nodes  [bucketSize]unsafe.Pointer
	next   unsafe.Pointer
	mutex  sync.Mutex
}

func (root *paddedBucket) isEmpty() bool {
	b := root
	for {
		for i := 0; i < bucketSize; i++ {
			if b.nodes[i] != nil {
				return false
			}
		}
		if b.next == nil {
			return true
		}
		b = (*paddedBucket)(b.next)
	}
}

func (root *paddedBucket) add(h uint64, nodePtr unsafe.Pointer) {
	b := root
	for {
		for i := 0; i < bucketSize; i++ {
			if b.nodes[i] == nil {
				b.hashes[i] = h
				b.nodes[i] = nodePtr
				return
			}
		}
		if b.next == nil {
			newBucket := &paddedBucket{}
			newBucket.hashes[0] = h
			newBucket.nodes[0] = nodePtr
			b.next = unsafe.Pointer(newBucket)
			return
		}
		b = (*paddedBucket)(b.next)
	}
}
