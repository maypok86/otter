package node

import (
	"sync"
	"sync/atomic"

	"github.com/maypok86/otter/internal/unixtime"
)

const (
	maxFrequency     = 3
	ghostFrequency   = int32(-1)
	defaultFrequency = int32(0)
)

var nodePool sync.Pool

type Node[K comparable, V any] struct {
	key        K
	value      V
	expiration uint64
	frequency  int32
}

func New[K comparable, V any](key K, value V, expiration uint64) *Node[K, V] {
	n := nodePool.Get()
	if n == nil {
		return &Node[K, V]{
			key:        key,
			value:      value,
			expiration: expiration,
		}
	}

	//nolint:errcheck // we check nil value (not found in pool or cast error)
	v := n.(*Node[K, V])
	v.key = key
	v.value = value
	v.expiration = expiration

	return v
}

func Free[K comparable, V any](n *Node[K, V]) {
	n.frequency = defaultFrequency
	nodePool.Put(n)
}

func (n *Node[K, V]) Key() K {
	return n.key
}

func (n *Node[K, V]) Value() V {
	return n.value
}

func (n *Node[K, V]) IsExpired() bool {
	return n.expiration != 0 && n.expiration < unixtime.Now()
}

func (n *Node[K, V]) IsGhost() bool {
	return atomic.LoadInt32(&n.frequency) == ghostFrequency
}

func (n *Node[K, V]) Touch() bool {
	for {
		frequency := atomic.LoadInt32(&n.frequency)
		if frequency >= maxFrequency {
			// don't need cas
			return true
		}
		if atomic.CompareAndSwapInt32(&n.frequency, frequency, minInt32(frequency+1, maxFrequency)) {
			return frequency != ghostFrequency // is not ghost
		}
	}
}

func (n *Node[K, V]) Untouch() bool {
	for {
		frequency := atomic.LoadInt32(&n.frequency)

		// touched
		if frequency > defaultFrequency {
			if atomic.CompareAndSwapInt32(&n.frequency, frequency, frequency-1) {
				return true
			}
			continue
		}

		return false
	}
}

func (n *Node[K, V]) BecomeGhost() bool {
	return atomic.CompareAndSwapInt32(&n.frequency, defaultFrequency, ghostFrequency)
}

func (n *Node[K, V]) MustGhost() {
	atomic.StoreInt32(&n.frequency, ghostFrequency)
}

func (n *Node[K, V]) Revive() bool {
	return atomic.CompareAndSwapInt32(&n.frequency, ghostFrequency, defaultFrequency)
}

func (n *Node[K, V]) Touched() bool {
	return atomic.LoadInt32(&n.frequency) > defaultFrequency
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
