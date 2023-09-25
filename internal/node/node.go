package node

import (
	"sync/atomic"

	"github.com/maypok86/otter/internal/unixtime"
)

const (
	maxFrequency     = 3
	ghostFrequency   = int32(-1)
	defaultFrequency = int32(0)
)

type Node[K comparable, V any] struct {
	key        K
	value      V
	expiration uint64
	frequency  atomic.Int32
}

func New[K comparable, V any](key K, value V, expiration uint64) *Node[K, V] {
	return &Node[K, V]{
		key:        key,
		value:      value,
		expiration: expiration,
	}
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
	return n.frequency.Load() == ghostFrequency
}

func (n *Node[K, V]) Touch() bool {
	for {
		frequency := n.frequency.Load()
		if frequency >= maxFrequency {
			// don't need cas
			return true
		}
		if n.frequency.CompareAndSwap(frequency, minInt32(frequency+1, maxFrequency)) {
			return frequency != ghostFrequency // is not ghost
		}
	}
}

func (n *Node[K, V]) Untouch() bool {
	for {
		frequency := n.frequency.Load()

		// touched
		if frequency > defaultFrequency {
			if n.frequency.CompareAndSwap(frequency, frequency-1) {
				return true
			}
			continue
		}

		return false
	}
}

func (n *Node[K, V]) BecomeGhost() bool {
	return n.frequency.CompareAndSwap(defaultFrequency, ghostFrequency)
}

func (n *Node[K, V]) MustGhost() {
	n.frequency.Store(ghostFrequency)
}

func (n *Node[K, V]) Revive() bool {
	return n.frequency.CompareAndSwap(ghostFrequency, defaultFrequency)
}

func (n *Node[K, V]) Touched() bool {
	return n.frequency.Load() > defaultFrequency
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
