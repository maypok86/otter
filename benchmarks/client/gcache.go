package client

import (
	"time"

	"github.com/bluele/gcache"
)

type Gcache[K comparable, V any] struct {
	client gcache.Cache
}

func (c *Gcache[K, V]) Init(capacity int) {
	client := gcache.New(capacity).Expiration(time.Hour).LRU().Build()
	c.client = client
}

func (c *Gcache[K, V]) Name() string {
	return "gcache"
}

func (c *Gcache[K, V]) Get(key K) (V, bool) {
	v, err := c.client.Get(key)
	if err != nil {
		var zero V
		return zero, false
	}
	return v.(V), true
}

func (c *Gcache[K, V]) Set(key K, value V) {
	if err := c.client.Set(key, value); err != nil {
		panic(err)
	}
}

func (c *Gcache[K, V]) Close() {
	c.client.Purge()
	c.client = nil
}
