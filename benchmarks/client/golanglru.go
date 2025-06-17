package client

import (
	lru "github.com/hashicorp/golang-lru/v2"
)

type GolangLRU[K comparable, V any] struct {
	client *lru.Cache[K, V]
}

func (c *GolangLRU[K, V]) Init(capacity int) {
	client, err := lru.New[K, V](capacity)
	if err != nil {
		panic(err)
	}
	c.client = client
}

func (c *GolangLRU[K, V]) Get(key K) (V, bool) {
	return c.client.Get(key)
}

func (c *GolangLRU[K, V]) Set(key K, value V) {
	c.client.Add(key, value)
}

func (c *GolangLRU[K, V]) Name() string {
	return "golang-lru"
}

func (c *GolangLRU[K, V]) Close() {
	c.client.Purge()
	c.client = nil
}
