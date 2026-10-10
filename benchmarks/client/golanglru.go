package client

import (
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

type GolangLRU[K comparable, V any] struct {
	// TTL enables expiration after write when it's positive. It switches
	// to the expirable LRU, because the plain one has no expiration.
	TTL       time.Duration
	client    *lru.Cache[K, V]
	expirable *expirable.LRU[K, V]
}

func (c *GolangLRU[K, V]) Init(capacity int) {
	if c.TTL > 0 {
		c.expirable = expirable.NewLRU[K, V](capacity, nil, c.TTL)
		return
	}
	client, err := lru.New[K, V](capacity)
	if err != nil {
		panic(err)
	}
	c.client = client
}

func (c *GolangLRU[K, V]) Get(key K) (V, bool) {
	if c.expirable != nil {
		return c.expirable.Get(key)
	}
	return c.client.Get(key)
}

func (c *GolangLRU[K, V]) Set(key K, value V) {
	if c.expirable != nil {
		c.expirable.Add(key, value)
		return
	}
	c.client.Add(key, value)
}

func (c *GolangLRU[K, V]) Name() string {
	return "golang-lru"
}

func (c *GolangLRU[K, V]) Close() {
	if c.expirable != nil {
		c.expirable.Purge()
		c.expirable = nil
		return
	}
	c.client.Purge()
	c.client = nil
}
