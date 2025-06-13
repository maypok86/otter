package product

import (
	lru "github.com/hashicorp/golang-lru/v2"
)

type LRU[K comparable, V any] struct {
	client *lru.Cache[K, V]
}

func (c *LRU[K, V]) Init(capacity int) {
	client, err := lru.New[K, V](capacity)
	if err != nil {
		panic(err)
	}
	c.client = client
}

func (c *LRU[K, V]) Get(key K) (V, bool) {
	return c.client.Get(key)
}

func (c *LRU[K, V]) Set(key K, value V) {
	c.client.Add(key, value)
}

func (c *LRU[K, V]) Name() string {
	return "lru"
}

func (c *LRU[K, V]) Close() {
	c.client = nil
}
