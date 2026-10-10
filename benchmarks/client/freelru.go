package client

import (
	"hash/maphash"
	"time"

	"github.com/elastic/go-freelru"
)

type FreeLRU[K comparable, V any] struct {
	// TTL enables expiration after write when it's positive.
	TTL    time.Duration
	client *freelru.ShardedLRU[K, V]
}

func (c *FreeLRU[K, V]) Init(capacity int) {
	// The library leaves the hash function to the caller.
	seed := maphash.MakeSeed()
	//nolint:gosec // capacity is small enough
	client, err := freelru.NewSharded[K, V](uint32(capacity), func(key K) uint32 {
		//nolint:gosec // only the lower bits are needed
		return uint32(maphash.Comparable(seed, key))
	})
	if err != nil {
		panic(err)
	}
	if c.TTL > 0 {
		client.SetLifetime(c.TTL)
	}
	c.client = client
}

func (c *FreeLRU[K, V]) Name() string {
	return "freelru"
}

func (c *FreeLRU[K, V]) Get(key K) (V, bool) {
	return c.client.Get(key)
}

func (c *FreeLRU[K, V]) Set(key K, value V) {
	c.client.Add(key, value)
}

func (c *FreeLRU[K, V]) Close() {
	c.client.Purge()
	c.client = nil
}
