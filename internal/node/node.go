package node

import (
	"sync"
	"sync/atomic"

	"github.com/maypok86/otter/internal/unixtime"
)

type Node[K comparable, V any] struct {
	key        K
	value      V
	mutex      sync.Mutex
	hash       uint64
	expiration uint64
	Meta       Meta
	cost       uint32
}

func New[K comparable, V any](key K, value V, expiration uint64, cost uint32) *Node[K, V] {
	return &Node[K, V]{
		key:        key,
		value:      value,
		expiration: expiration,
		Meta:       DefaultMeta(),
		cost:       cost,
	}
}

func (n *Node[K, V]) Key() K {
	return n.key
}

func (n *Node[K, V]) Value() V {
	n.mutex.Lock()
	v := n.value
	n.mutex.Unlock()
	return v
}

func (n *Node[K, V]) SetValue(value V) {
	n.mutex.Lock()
	n.value = value
	n.mutex.Unlock()
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

func (n *Node[K, V]) Expiration() uint64 {
	return n.expiration
}

func (n *Node[K, V]) Cost() uint32 {
	return atomic.LoadUint32(&n.cost)
}

func (n *Node[K, V]) SwapCost(cost uint32) (old uint32) {
	return atomic.SwapUint32(&n.cost, cost)
}
