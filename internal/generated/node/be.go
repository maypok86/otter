// Code generated by NodeGenerator. DO NOT EDIT.

// Package node is a generated by the generator.
package node

import (
	"sync/atomic"
	"unsafe"
)

// BE is a cache entry that provide the following features:
//
// 1. Base
//
// 2. Expiration
type BE[K comparable, V any] struct {
	key        K
	value      V
	prevExp    *BE[K, V]
	nextExp    *BE[K, V]
	expiration int64
	state      uint32
}

// NewBE creates a new BE.
func NewBE[K comparable, V any](key K, value V, expiration int64, weight uint32) Node[K, V] {
	return &BE[K, V]{
		key:        key,
		value:      value,
		expiration: expiration,
		state:      aliveState,
	}
}

// CastPointerToBE casts a pointer to BE.
func CastPointerToBE[K comparable, V any](ptr unsafe.Pointer) Node[K, V] {
	return (*BE[K, V])(ptr)
}

func (n *BE[K, V]) Key() K {
	return n.key
}

func (n *BE[K, V]) Value() V {
	return n.value
}

func (n *BE[K, V]) AsPointer() unsafe.Pointer {
	return unsafe.Pointer(n)
}

func (n *BE[K, V]) Prev() Node[K, V] {
	panic("not implemented")
}

func (n *BE[K, V]) SetPrev(v Node[K, V]) {
	panic("not implemented")
}

func (n *BE[K, V]) Next() Node[K, V] {
	panic("not implemented")
}

func (n *BE[K, V]) SetNext(v Node[K, V]) {
	panic("not implemented")
}

func (n *BE[K, V]) PrevExp() Node[K, V] {
	return n.prevExp
}

func (n *BE[K, V]) SetPrevExp(v Node[K, V]) {
	if v == nil {
		n.prevExp = nil
		return
	}
	n.prevExp = (*BE[K, V])(v.AsPointer())
}

func (n *BE[K, V]) NextExp() Node[K, V] {
	return n.nextExp
}

func (n *BE[K, V]) SetNextExp(v Node[K, V]) {
	if v == nil {
		n.nextExp = nil
		return
	}
	n.nextExp = (*BE[K, V])(v.AsPointer())
}

func (n *BE[K, V]) HasExpired(now int64) bool {
	return n.expiration <= now
}

func (n *BE[K, V]) Expiration() int64 {
	return n.expiration
}

func (n *BE[K, V]) Weight() uint32 {
	return 1
}

func (n *BE[K, V]) IsAlive() bool {
	return atomic.LoadUint32(&n.state) == aliveState
}

func (n *BE[K, V]) Die() {
	atomic.StoreUint32(&n.state, deadState)
}

func (n *BE[K, V]) Frequency() uint8 {
	panic("not implemented")
}

func (n *BE[K, V]) IncrementFrequency() {
	panic("not implemented")
}

func (n *BE[K, V]) DecrementFrequency() {
	panic("not implemented")
}

func (n *BE[K, V]) ResetFrequency() {
	panic("not implemented")
}

func (n *BE[K, V]) MarkSmall() {
	panic("not implemented")
}

func (n *BE[K, V]) IsSmall() bool {
	panic("not implemented")
}

func (n *BE[K, V]) MarkMain() {
	panic("not implemented")
}

func (n *BE[K, V]) IsMain() bool {
	panic("not implemented")
}

func (n *BE[K, V]) Unmark() {
	panic("not implemented")
}
