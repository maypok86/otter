package node

import (
	"sync/atomic"

	"github.com/maypok86/otter/internal/spinlock"
	"github.com/maypok86/otter/internal/unixtime"
)

const (
	unknownQueueType uint8 = iota
	smallQueueType
	mainQueueType

	maxFrequency uint8 = 3
)

type Node[K comparable, V any] struct {
	key        K
	value      V
	prev       *Node[K, V]
	next       *Node[K, V]
	lock       spinlock.SpinLock
	expiration uint32
	hash       uint64
	cost       uint32
	frequency  uint8
	queueType  uint8
}

func New[K comparable, V any](key K, value V, expiration, cost uint32) *Node[K, V] {
	return &Node[K, V]{
		key:        key,
		value:      value,
		expiration: expiration,
		cost:       cost,
	}
}

func (n *Node[K, V]) Key() K {
	return n.key
}

func (n *Node[K, V]) Value() V {
	n.lock.Lock()
	v := n.value
	n.lock.Unlock()
	return v
}

func (n *Node[K, V]) SetValue(value V) {
	n.lock.Lock()
	n.value = value
	n.lock.Unlock()
}

func (n *Node[K, V]) Hash() uint64 {
	return n.hash
}

func (n *Node[K, V]) SetHash(h uint64) {
	n.hash = h
}

func (n *Node[K, V]) IsExpired() bool {
	return n.expiration > 0 && n.expiration < unixtime.Now()
}

func (n *Node[K, V]) Expiration() uint32 {
	return n.expiration
}

func (n *Node[K, V]) Cost() uint32 {
	return atomic.LoadUint32(&n.cost)
}

func (n *Node[K, V]) SwapCost(cost uint32) (old uint32) {
	return atomic.SwapUint32(&n.cost, cost)
}

func (n *Node[K, V]) Frequency() uint8 {
	return n.frequency
}

func (n *Node[K, V]) IncrementFrequency() {
	n.frequency = minUint8(n.frequency+1, maxFrequency)
}

func (n *Node[K, V]) DecrementFrequency() {
	n.frequency--
}

func (n *Node[K, V]) ResetFrequency() {
	n.frequency = 0
}

func (n *Node[K, V]) MarkSmall() {
	n.queueType = smallQueueType
}

func (n *Node[K, V]) IsSmall() bool {
	return n.queueType == smallQueueType
}

func (n *Node[K, V]) MarkMain() {
	n.queueType = mainQueueType
}

func (n *Node[K, V]) IsMain() bool {
	return n.queueType == mainQueueType
}

func (n *Node[K, V]) Unmark() {
	n.queueType = unknownQueueType
}

func minUint8(a, b uint8) uint8 {
	if a < b {
		return a
	}

	return b
}
