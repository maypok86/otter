package client

import (
	"runtime"
	"time"

	"github.com/karlseguin/ccache/v3"
)

type Ccache[V any] struct {
	client *ccache.Cache[V]
}

func (c *Ccache[V]) Init(capacity int) {
	client := ccache.New(
		ccache.Configure[V]().
			MaxSize(int64(capacity)).
			//nolint:gosec // there will never be an overflow
			Buckets(uint32(16 * runtime.GOMAXPROCS(0))),
	)
	c.client = client
}

func (c *Ccache[V]) Name() string {
	return "ccache"
}

func (c *Ccache[V]) Get(key string) (V, bool) {
	item := c.client.Get(key)
	if item == nil || item.Expired() {
		var value V
		return value, false
	}

	return item.Value(), true
}

func (c *Ccache[V]) Set(key string, value V) {
	c.client.Set(key, value, time.Hour)
}

func (c *Ccache[V]) Close() {
	c.client.Clear()
	c.client.Stop()
	c.client = nil
}
