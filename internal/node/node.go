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
	Key        K
	Value      V
	Expiration uint64
	Frequency  atomic.Int32
}

func New[K comparable, V any](key K, value V, expiration uint64) *Node[K, V] {
	return &Node[K, V]{
		Key:        key,
		Value:      value,
		Expiration: expiration,
	}
}

func (n *Node[K, V]) IsExpired() bool {
	return n.Expiration != 0 && n.Expiration < unixtime.Now()
}

func (n *Node[K, V]) IsGhost() bool {
	return n.Frequency.Load() == ghostFrequency
}

func (n *Node[K, V]) Touch() bool {
	for {
		frequency := n.Frequency.Load()
		if frequency >= maxFrequency {
			// don't need cas
			return true
		}
		if n.Frequency.CompareAndSwap(frequency, minInt32(frequency+1, maxFrequency)) {
			return frequency != ghostFrequency // is not ghost
		}
	}
}

func (n *Node[K, V]) Untouch() bool {
	for {
		frequency := n.Frequency.Load()

		// touched
		if frequency > defaultFrequency {
			if n.Frequency.CompareAndSwap(frequency, frequency-1) {
				return true
			}
			continue
		}

		return false
	}
}

func (n *Node[K, V]) BecomeGhost() bool {
	return n.Frequency.CompareAndSwap(defaultFrequency, ghostFrequency)
}

func (n *Node[K, V]) MustGhost() {
	n.Frequency.Store(ghostFrequency)
}

func (n *Node[K, V]) Revive() bool {
	return n.Frequency.CompareAndSwap(ghostFrequency, defaultFrequency)
}

func (n *Node[K, V]) Touched() bool {
	return n.Frequency.Load() > defaultFrequency
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
