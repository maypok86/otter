package client

import (
	"time"

	"github.com/jellydator/ttlcache/v3"
)

type TTLCache[K comparable, V any] struct {
	// TTL enables expiration after write when it's positive.
	TTL    time.Duration
	client *ttlcache.Cache[K, V]
}

func (c *TTLCache[K, V]) options(capacity int) []ttlcache.Option[K, V] {
	opts := []ttlcache.Option[K, V]{
		ttlcache.WithCapacity[K, V](uint64(capacity)),
	}
	if c.TTL > 0 {
		// Reads extend the lifetime by default, the other caches don't.
		opts = append(opts, ttlcache.WithTTL[K, V](c.TTL), ttlcache.WithDisableTouchOnHit[K, V]())
	}
	return opts
}

func (c *TTLCache[K, V]) start(opts []ttlcache.Option[K, V]) {
	c.client = ttlcache.New[K, V](opts...)
	go c.client.Start()
}

func (c *TTLCache[K, V]) Init(capacity int) {
	c.start(c.options(capacity))
}

func (c *TTLCache[K, V]) InitLoading(capacity int, load func(K) V) {
	loader := ttlcache.LoaderFunc[K, V](func(cache *ttlcache.Cache[K, V], key K) *ttlcache.Item[K, V] {
		return cache.Set(key, load(key), ttlcache.DefaultTTL)
	})
	opts := append(c.options(capacity), ttlcache.WithLoader[K, V](ttlcache.NewSuppressedLoader[K, V](loader, nil)))
	c.start(opts)
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

func (c *TTLCache[K, V]) Load(key K) V {
	return c.client.Get(key).Value()
}

func (c *TTLCache[K, V]) Set(key K, value V) {
	c.client.Set(key, value, ttlcache.DefaultTTL)
}

func (c *TTLCache[K, V]) Close() {
	c.client.Stop()
	c.client.DeleteAll()
	c.client = nil
}
