package node

import (
	"sync"
	"sync/atomic"

	"github.com/maypok86/otter/internal/unixtime"
)

const (
	defaultFrequency = int32(0)
)

type Node[K comparable, V any] struct {
	mutex      sync.Mutex
	key        K
	value      V
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

func (n *Node[K, V]) SwapExpiration(expiration uint64) (old uint64) {
	return atomic.SwapUint64(&n.expiration, expiration)
}

func (n *Node[K, V]) IsExpired() bool {
	expiration := atomic.LoadUint64(&n.expiration)
	return expiration > 0 && expiration < unixtime.Now()
}

func (n *Node[K, V]) Cost() uint32 {
	return atomic.LoadUint32(&n.cost)
}

func (n *Node[K, V]) SwapCost(cost uint32) (old uint32) {
	return atomic.SwapUint32(&n.cost, cost)
}
