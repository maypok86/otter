package client

import (
	"context"
	"time"

	"github.com/viccon/sturdyc"
)

type Sturdyc[V any] struct {
	// TTL sets the expiration after write. sturdyc always expires entries,
	// so a non-positive TTL means an hour.
	TTL    time.Duration
	client *sturdyc.Client[V]
	fetch  func(key string) sturdyc.FetchFn[V]
}

func (c *Sturdyc[V]) Init(capacity int) {
	ttl := c.TTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	c.client = sturdyc.New[V](capacity, 10, ttl, 10)
}

func (c *Sturdyc[V]) InitLoading(capacity int, load func(string) V) {
	c.Init(capacity)
	c.fetch = func(key string) sturdyc.FetchFn[V] {
		return func(context.Context) (V, error) {
			return load(key), nil
		}
	}
}

func (c *Sturdyc[V]) Name() string {
	return "sturdyc"
}

func (c *Sturdyc[V]) Get(key string) (V, bool) {
	return c.client.Get(key)
}

func (c *Sturdyc[V]) Load(key string) V {
	// The API takes the fetch function per call, so it has to capture the key.
	v, err := c.client.GetOrFetch(context.Background(), key, c.fetch(key))
	if err != nil {
		panic(err)
	}
	return v
}

func (c *Sturdyc[V]) Set(key string, value V) {
	c.client.Set(key, value)
}

func (c *Sturdyc[V]) Close() {
	c.client = nil
	c.fetch = nil
}
