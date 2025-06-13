package client

import (
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
)

type GolangLRU[K comparable, V any] struct {
	client *expirable.LRU[K, V]
}

func (c *GolangLRU[K, V]) Init(capacity int) {
	client := expirable.NewLRU[K, V](capacity, nil, time.Hour)
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
