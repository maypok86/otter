package client

import (
	"context"
	"time"

	"github.com/Yiling-J/theine-go"
)

type Theine[K comparable, V any] struct {
	// TTL enables expiration after write when it's positive.
	TTL     time.Duration
	client  *theine.Cache[K, V]
	loading *theine.LoadingCache[K, V]
}

func (c *Theine[K, V]) Init(capacity int) {
	client, err := theine.NewBuilder[K, V](int64(capacity)).Build()
	if err != nil {
		panic(err)
	}
	c.client = client
}

func (c *Theine[K, V]) InitLoading(capacity int, load func(K) V) {
	loading, err := theine.NewBuilder[K, V](int64(capacity)).
		Loading(func(_ context.Context, key K) (theine.Loaded[V], error) {
			return theine.Loaded[V]{Value: load(key), Cost: 1, TTL: c.TTL}, nil
		}).
		Build()
	if err != nil {
		panic(err)
	}
	c.loading = loading
}

func (c *Theine[K, V]) Name() string {
	return "theine"
}

func (c *Theine[K, V]) Get(key K) (V, bool) {
	return c.client.Get(key)
}

func (c *Theine[K, V]) Load(key K) V {
	v, err := c.loading.Get(context.Background(), key)
	if err != nil {
		panic(err)
	}
	return v
}

func (c *Theine[K, V]) Set(key K, value V) {
	if c.TTL > 0 {
		c.client.SetWithTTL(key, value, 1, c.TTL)
		return
	}
	c.client.Set(key, value, 1)
}

func (c *Theine[K, V]) Wait() {
	c.client.Wait()
}

func (c *Theine[K, V]) Close() {
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}
	if c.loading != nil {
		c.loading.Close()
		c.loading = nil
	}
}
