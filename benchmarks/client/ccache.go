package client

import (
	"runtime"
	"time"

	"github.com/karlseguin/ccache/v3"
)

type Ccache[V any] struct {
	// TTL sets the expiration after write. ccache always expires entries,
	// so a non-positive TTL means an hour.
	TTL    time.Duration
	client *ccache.Cache[V]
	ttl    time.Duration
	load   func(string) V
}

func (c *Ccache[V]) Init(capacity int) {
	client := ccache.New(
		ccache.Configure[V]().
			MaxSize(int64(capacity)).
			//nolint:gosec // there will never be an overflow
			Buckets(uint32(16 * runtime.GOMAXPROCS(0))),
	)
	c.client = client
	c.ttl = c.TTL
	if c.ttl <= 0 {
		c.ttl = time.Hour
	}
}

func (c *Ccache[V]) InitLoading(capacity int, load func(string) V) {
	c.Init(capacity)
	c.load = load
}

func (c *Ccache[V]) Name() string {
	return "ccache"
}

func (c *Ccache[V]) Get(key string) (V, bool) {
	item := c.client.Get(key)
	if item == nil {
		var value V
		return value, false
	}

	return item.Value(), true
}

func (c *Ccache[V]) Load(key string) V {
	// Fetch doesn't deduplicate concurrent loads of the same key.
	item, err := c.client.Fetch(key, c.ttl, func() (V, error) {
		return c.load(key), nil
	})
	if err != nil {
		panic(err)
	}
	return item.Value()
}

func (c *Ccache[V]) Set(key string, value V) {
	c.client.Set(key, value, c.ttl)
}

func (c *Ccache[V]) Close() {
	c.client.Clear()
	c.client.Stop()
	c.client = nil
	c.load = nil
}
