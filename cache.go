package otter

import (
	"sync"
	"time"
	"unsafe"

	"github.com/zeebo/xxh3"

	"github.com/maypok86/otter/internal/eviction/s3fifo"
	"github.com/maypok86/otter/internal/node"
	"github.com/maypok86/otter/internal/shard"
	"github.com/maypok86/otter/internal/stats"
	"github.com/maypok86/otter/internal/unixtime"
)

func zeroValue[V any]() V {
	var zero V
	return zero
}

type Cache[K comparable, V any] struct {
	shards      []*shard.Shard[K, V]
	policy      *s3fifo.Policy[K, V]
	stats       *stats.Stats
	closeOnce   sync.Once
	keyIsString bool
	keySize     int
	mask        uint64
	capacity    int
}

func New[K comparable, V any](opts ...Option) (*Cache[K, V], error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	if err := o.validate(); err != nil {
		return nil, err
	}

	shardCapacity := (o.capacity + o.shardCount - 1) / o.shardCount
	shards := make([]*shard.Shard[K, V], 0, o.shardCount)
	for i := 0; i < o.shardCount; i++ {
		shards = append(shards, shard.New[K, V](shard.WithNodeCount[K](shardCapacity)))
	}

	c := &Cache[K, V]{
		shards:   shards,
		mask:     uint64(o.shardCount - 1),
		capacity: o.capacity,
	}

	c.policy = s3fifo.NewPolicy[K, V](o.capacity, func(n *node.Node[K, V]) {
		c.getShard(n.Key()).EvictNode(n)
		node.Free(n)
	})
	if o.statsEnabled {
		c.stats = stats.New()
	}

	var key K
	switch (any(key)).(type) {
	case string:
		c.keyIsString = true
	default:
		c.keySize = int(unsafe.Sizeof(key))
	}

	unixtime.Start()

	return c, nil
}

func (c *Cache[K, V]) getShard(key K) *shard.Shard[K, V] {
	var strKey string
	if c.keyIsString {
		strKey = *(*string)(unsafe.Pointer(&key))
	} else {
		strKey = *(*string)(unsafe.Pointer(&struct {
			data unsafe.Pointer
			len  int
		}{unsafe.Pointer(&key), c.keySize}))
	}

	idx := int(xxh3.HashString(strKey) & c.mask)

	return c.shards[idx]
}

func (c *Cache[K, V]) Has(key K) bool {
	_, ok := c.Get(key)
	return ok
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	got, evicted, ok := c.getShard(key).Get(key)
	if evicted != nil {
		c.policy.Delete(evicted)
	}
	if !ok {
		c.stats.IncrementMisses()
		return zeroValue[V](), false
	}

	c.policy.Get(got)
	c.stats.IncrementHits()

	return got.Value(), ok
}

func (c *Cache[K, V]) Set(key K, value V) {
	c.set(key, value, 0)
}

func (c *Cache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	expiration := unixtime.Now() + uint64(ttl/time.Second)
	c.set(key, value, expiration)
}

func (c *Cache[K, V]) set(key K, value V, expiration uint64) {
	inserted, evicted := c.getShard(key).SetWithExpiration(key, value, expiration)
	if inserted != nil {
		c.policy.Add(inserted)
	}
	if evicted != nil {
		c.policy.Delete(evicted)
	}
}

func (c *Cache[K, V]) Delete(key K) {
	deleted := c.getShard(key).Delete(key)
	if deleted != nil {
		c.policy.Delete(deleted)
	}
}

func (c *Cache[K, V]) Clear() {
	for i := 0; i < len(c.shards); i++ {
		c.shards[i].Clear()
	}
	c.policy.Clear()
	c.stats.Clear()
}

func (c *Cache[K, V]) Close() {
	c.closeOnce.Do(func() {
		c.Clear()
		unixtime.Stop()
	})
}

func (c *Cache[K, V]) Capacity() int {
	return c.capacity
}

func (c *Cache[K, V]) Hits() int64 {
	return c.stats.Hits()
}

func (c *Cache[K, V]) Misses() int64 {
	return c.stats.Misses()
}

func (c *Cache[K, V]) Ratio() float64 {
	return c.stats.Ratio()
}
