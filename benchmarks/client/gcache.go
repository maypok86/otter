package client

import (
	"time"

	"github.com/bluele/gcache"
)

type Gcache[K comparable, V any] struct {
	// TTL enables expiration after write when it's positive.
	TTL    time.Duration
	client gcache.Cache
}

func (c *Gcache[K, V]) builder(capacity int) *gcache.CacheBuilder {
	b := gcache.New(capacity).LRU()
	if c.TTL > 0 {
		b = b.Expiration(c.TTL)
	}
	return b
}

func (c *Gcache[K, V]) Init(capacity int) {
	c.client = c.builder(capacity).Build()
}

func (c *Gcache[K, V]) InitLoading(capacity int, load func(K) V) {
	c.client = c.builder(capacity).
		LoaderFunc(func(key any) (any, error) {
			//nolint:errcheck // the cache only gets keys of type K
			return load(key.(K)), nil
		}).
		Build()
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

func (c *Gcache[K, V]) Load(key K) V {
	v, err := c.client.Get(key)
	if err != nil {
		panic(err)
	}
	//nolint:errcheck // the loader only returns values of type V
	return v.(V)
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
