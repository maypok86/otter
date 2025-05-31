package client

import (
	"time"

	"github.com/jellydator/ttlcache/v3"
)

type TTLCache[K comparable, V any] struct {
	client *ttlcache.Cache[K, V]
}

func (c *TTLCache[K, V]) Init(capacity int) {
	client := ttlcache.New[K, V](
		ttlcache.WithTTL[K, V](time.Hour),
		ttlcache.WithCapacity[K, V](uint64(capacity)),
	)
	go client.Start()
	c.client = client
}

func (c *TTLCache[K, V]) Name() string {
	return "ttlcache"
}

func (c *TTLCache[K, V]) Get(key K) (V, bool) {
	i := c.client.Get(key)
	if i == nil || i.IsExpired() {
		var zero V
		return zero, false
	}
	return i.Value(), true
}

func (c *TTLCache[K, V]) Set(key K, value V) {
	c.client.Set(key, value, ttlcache.DefaultTTL)
}

func (c *TTLCache[K, V]) Close() {
	c.client.Stop()
	c.client.DeleteAll()
	c.client = nil
}
