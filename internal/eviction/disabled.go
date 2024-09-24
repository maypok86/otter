package eviction

import (
	"github.com/maypok86/otter/v2/internal/generated/node"
)

type Disabled[K comparable, V any] struct{}

func NewDisabled[K comparable, V any]() Disabled[K, V] {
	return Disabled[K, V]{}
}

func (d Disabled[K, V]) Read(nodes node.Node[K, V]) {
	panic("not implemented")
}

func (d Disabled[K, V]) Add(n node.Node[K, V], nowNanos int64, evictNode func(n node.Node[K, V], nowNanos int64)) {
}

func (d Disabled[K, V]) Delete(n node.Node[K, V]) {
}

func (d Disabled[K, V]) Clear() {
}
