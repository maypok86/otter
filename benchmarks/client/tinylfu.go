package client

import (
	"hash/maphash"

	"github.com/dgryski/go-tinylfu"
)

type TinyLFU[K comparable, V any] struct {
	client *tinylfu.T[K, V]
}

func (c *TinyLFU[K, V]) Init(capacity int) {
	seed := maphash.MakeSeed()
	client := tinylfu.New[K, V](capacity, 10*capacity, func(k K) uint64 {
		return maphash.Comparable(seed, k)
	})
	c.client = client
}

func (c *TinyLFU[K, V]) Name() string {
	return "tinylfu"
}

func (c *TinyLFU[K, V]) Get(key K) (V, bool) {
	return c.client.Get(key)
}

func (c *TinyLFU[K, V]) Set(key K, value V) {
	c.client.Add(key, value)
}

func (c *TinyLFU[K, V]) Close() {
	c.client = nil
}
