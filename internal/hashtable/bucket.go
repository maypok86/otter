package hashtable

import (
	"sync"
	"unsafe"

	"github.com/maypok86/otter/internal/xruntime"
)

// <= 2 cache lines.
type paddedBucket struct {
	padding [2*xruntime.CacheLineSize - unsafe.Sizeof(bucket{})]byte

	bucket
}

type bucket struct {
	mutex  sync.Mutex
	seq    uint64
	hashes [bucketSize]uint64
	nodes  [bucketSize]unsafe.Pointer
}

func (b *paddedBucket) isEmpty() bool {
	for i := 0; i < bucketSize; i++ {
		if b.nodes[i] != nil {
			return false
		}
	}
	return true
}

func (b *paddedBucket) add(h uint64, nodePtr unsafe.Pointer) {
	for i := 0; i < bucketSize; i++ {
		if b.nodes[i] == nil {
			b.hashes[i] = h
			b.nodes[i] = nodePtr
			return
		}
	}
}
