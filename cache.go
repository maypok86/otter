package otter

import (
	"sync"
	"time"

	"github.com/maypok86/otter/internal/expire"
	"github.com/maypok86/otter/internal/hashtable"
	"github.com/maypok86/otter/internal/lossy"
	"github.com/maypok86/otter/internal/node"
	"github.com/maypok86/otter/internal/queue"
	"github.com/maypok86/otter/internal/s3fifo"
	"github.com/maypok86/otter/internal/stats"
	"github.com/maypok86/otter/internal/unixtime"
	"github.com/maypok86/otter/internal/xmath"
	"github.com/maypok86/otter/internal/xruntime"
)

func zeroValue[V any]() V {
	var zero V
	return zero
}

type Config[K comparable, V any] struct {
	Capacity     int
	ShardCount   int
	StatsEnabled bool
	CostFunc     func(key K, value V) uint32
}

type Cache[K comparable, V any] struct {
	shards       []*hashtable.Map[K, V]
	policy       *s3fifo.Policy[K, V]
	expirePolicy *expire.Policy[K, V]
	stats        *stats.Stats
	readBuffers  []*lossy.Buffer[node.Node[K, V]]
	writeBuffer  *queue.MPSC[node.WriteItem[K, V]]
	closeOnce    sync.Once
	hasher       *hasher[K]
	costFunc     func(key K, value V) uint32
	mask         uint64
	capacity     int
}

func NewCache[K comparable, V any](c Config[K, V]) *Cache[K, V] {
	shards := make([]*hashtable.Map[K, V], 0, c.ShardCount)
	readBuffers := make([]*lossy.Buffer[node.Node[K, V]], 0, c.ShardCount)
	for i := 0; i < c.ShardCount; i++ {
		shards = append(shards, hashtable.New[K, V]())
		readBuffers = append(readBuffers, lossy.New[node.Node[K, V]]())
	}

	cache := &Cache[K, V]{
		shards:      shards,
		policy:      s3fifo.NewPolicy[K, V](uint32(c.Capacity)),
		readBuffers: readBuffers,
		writeBuffer: queue.NewMPSC[node.WriteItem[K, V]](128 * int(xmath.RoundUpPowerOf2(xruntime.Parallelism()))),
		hasher:      newHasher[K](),
		mask:        uint64(c.ShardCount - 1),
		costFunc:    c.CostFunc,
		capacity:    c.Capacity,
	}

	cache.expirePolicy = expire.NewPolicy[K, V](func(n *node.Node[K, V]) {
		cache.shards[cache.getShardIdx(n.Key())].EvictNode(n)
	})
	if c.StatsEnabled {
		cache.stats = stats.New()
	}

	unixtime.Start()
	go cache.process()

	return cache
}

func (c *Cache[K, V]) getShardIdx(key K) int {
	return int(c.hasher.hash(key) & c.mask)
}

func (c *Cache[K, V]) Has(key K) bool {
	_, ok := c.Get(key)
	return ok
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	idx := c.getShardIdx(key)
	got, ok := c.shards[idx].Get(key)
	if !ok {
		c.stats.IncMisses()
		return zeroValue[V](), false
	}

	if got.IsExpired() {
		c.writeBuffer.Insert(node.NewEvictedItem(got))
		c.stats.IncMisses()
		return zeroValue[V](), false
	}

	c.afterGet(idx, got)
	c.stats.IncHits()

	return got.Value(), ok
}

func (c *Cache[K, V]) afterGet(idx int, got *node.Node[K, V]) {
	pb := c.readBuffers[idx].Add(got)
	if pb != nil {
		deleted := c.policy.Read(pb.Deleted, pb.Returned)
		for _, n := range deleted {
			c.shards[c.getShardIdx(n.Key())].EvictNode(n)
		}
		c.readBuffers[idx].Free()
	}
}

func (c *Cache[K, V]) Set(key K, value V) {
	c.set(key, value, 0)
}

func (c *Cache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	ttl = (ttl + time.Second - 1) / time.Second
	expiration := unixtime.Now() + uint64(ttl)
	c.set(key, value, expiration)
}

func (c *Cache[K, V]) set(key K, value V, expiration uint64) {
	cost := c.costFunc(key, value)
	if cost >= c.policy.MaxAvailableCost() {
		return
	}

	idx := c.getShardIdx(key)
	s := c.shards[idx]
	got, ok := s.Get(key)
	if ok {
		if !got.IsExpired() {
			oldCost := got.SwapCost(cost)
			got.SetValue(value)
			costDiff := cost - oldCost
			if costDiff != 0 {
				c.writeBuffer.Insert(node.NewUpdatedItem(got, costDiff))
			}

			return
		}

		c.writeBuffer.Insert(node.NewEvictedItem(got))
	}

	n := node.New(key, value, expiration, cost)
	evicted := s.Set(n)
	// TODO: try insert?
	c.writeBuffer.Insert(node.NewAddedItem(n))
	if evicted != nil {
		c.writeBuffer.Insert(node.NewEvictedItem(evicted))
	}
}

func (c *Cache[K, V]) Delete(key K) {
	deleted := c.shards[c.getShardIdx(key)].Delete(key)
	if deleted != nil {
		c.writeBuffer.Insert(node.NewDeletedItem(deleted))
	}
}

func (c *Cache[K, V]) process() {
	bufferCapacity := 128
	buffer := make([]node.WriteItem[K, V], 0, bufferCapacity)
	deleted := make([]*node.Node[K, V], 0, bufferCapacity)
	expired := make([]*node.Node[K, V], 0, bufferCapacity)
	i := 0
	for {
		item := c.writeBuffer.Remove()

		if item.IsDeleted() || item.IsEvicted() {
			c.expirePolicy.Delete(item.GetNode())
		} else if item.IsAdded() {
			c.expirePolicy.Add(item.GetNode())
		}

		buffer = append(buffer, item)
		i++
		if i >= bufferCapacity {
			i -= bufferCapacity

			e := c.expirePolicy.RemoveExpired(expired)

			d := c.policy.Write(deleted, e, buffer)
			for _, n := range d {
				c.shards[c.getShardIdx(n.Key())].EvictNode(n)
			}

			buffer = buffer[:0]
			deleted = deleted[:0]
			expired = expired[:0]
		}
	}
}

func (c *Cache[K, V]) Clear() {
	for i := 0; i < len(c.shards); i++ {
		c.shards[i].Clear()
	}
	c.policy.Clear()
	c.expirePolicy.Clear()
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
