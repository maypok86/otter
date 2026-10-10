package client

import (
	"context"
	"time"

	"github.com/maypok86/otter/v2"
)

type Otter[K comparable, V any] struct {
	// TTL enables expiration after write when it's positive.
	TTL    time.Duration
	client *otter.Cache[K, V]
	loader otter.Loader[K, V]
}

func (c *Otter[K, V]) Init(capacity int) {
	opts := &otter.Options[K, V]{
		MaximumSize:     capacity,
		InitialCapacity: capacity,
	}
	if c.TTL > 0 {
		opts.ExpiryCalculator = otter.ExpiryWriting[K, V](c.TTL)
	}
	c.client = otter.Must(opts)
}

func (c *Otter[K, V]) InitLoading(capacity int, load func(K) V) {
	c.Init(capacity)
	c.loader = otter.LoaderFunc[K, V](func(_ context.Context, key K) (V, error) {
		return load(key), nil
	})
}

func (c *Otter[K, V]) Name() string {
	return "otter"
}

func (c *Otter[K, V]) Get(key K) (V, bool) {
	return c.client.GetIfPresent(key)
}

func (c *Otter[K, V]) Load(key K) V {
	v, err := c.client.Get(context.Background(), key, c.loader)
	if err != nil {
		panic(err)
	}
	return v
}

func (c *Otter[K, V]) Set(key K, value V) {
	c.client.Set(key, value)
}

func (c *Otter[K, V]) Close() {
	c.client = nil
	c.loader = nil
}
