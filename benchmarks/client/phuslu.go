package client

import (
	"context"
	"time"

	"github.com/phuslu/lru"
)

type Phuslu[K comparable, V any] struct {
	// TTL enables expiration after write when it's positive. It switches
	// to the TTL cache, because the plain one has no expiration.
	TTL     time.Duration
	client  *lru.LRUCache[K, V]
	ttl     *lru.TTLCache[K, V]
	load    func(context.Context, K) (V, error)
	loadTTL func(context.Context, K) (V, time.Duration, error)
}

func (c *Phuslu[K, V]) Init(capacity int) {
	if c.TTL > 0 {
		c.ttl = lru.NewTTLCache[K, V](capacity)
		return
	}
	c.client = lru.NewLRUCache[K, V](capacity)
}

func (c *Phuslu[K, V]) InitLoading(capacity int, load func(K) V) {
	c.Init(capacity)
	c.load = func(_ context.Context, key K) (V, error) {
		return load(key), nil
	}
	c.loadTTL = func(_ context.Context, key K) (V, time.Duration, error) {
		return load(key), c.TTL, nil
	}
}

func (c *Phuslu[K, V]) Name() string {
	return "phuslu"
}

func (c *Phuslu[K, V]) Get(key K) (V, bool) {
	if c.ttl != nil {
		return c.ttl.Get(key)
	}
	return c.client.Get(key)
}

func (c *Phuslu[K, V]) Load(key K) V {
	var (
		v   V
		err error
	)
	if c.ttl != nil {
		v, err, _ = c.ttl.GetOrLoad(context.Background(), key, c.loadTTL)
	} else {
		v, err, _ = c.client.GetOrLoad(context.Background(), key, c.load)
	}
	if err != nil {
		panic(err)
	}
	return v
}

func (c *Phuslu[K, V]) Set(key K, value V) {
	if c.ttl != nil {
		c.ttl.Set(key, value, c.TTL)
		return
	}
	c.client.Set(key, value)
}

func (c *Phuslu[K, V]) Close() {
	c.client = nil
	c.ttl = nil
	c.load = nil
	c.loadTTL = nil
}
