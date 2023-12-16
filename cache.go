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
	StatsEnabled bool
	CostFunc     func(key K, value V) uint32
}

type Cache[K comparable, V any] struct {
	hashmap       *hashtable.Map[K, V]
	policy        *s3fifo.Policy[K, V]
	expirePolicy  *expire.Policy[K, V]
	stats         *stats.Stats
	readBuffers   []*lossy.Buffer[node.Node[K, V]]
	writeBuffer   *queue.MPSC[node.WriteTask[K, V]]
	evictionMutex sync.Mutex
	closeOnce     sync.Once
	isClosed      bool
	doneClear     chan struct{}
	costFunc      func(key K, value V) uint32
	capacity      int
	mask          uint32
}

func NewCache[K comparable, V any](c Config[K, V]) *Cache[K, V] {
	parallelism := xruntime.Parallelism()
	roundedParallelism := int(xmath.RoundUpPowerOf2(parallelism))
	writeBufferCapacity := 128 * roundedParallelism
	readBuffersCount := 4 * roundedParallelism

	readBuffers := make([]*lossy.Buffer[node.Node[K, V]], 0, readBuffersCount)
	for i := 0; i < readBuffersCount; i++ {
		readBuffers = append(readBuffers, lossy.New[node.Node[K, V]]())
	}

	cache := &Cache[K, V]{
		hashmap:     hashtable.New[K, V](),
		policy:      s3fifo.NewPolicy[K, V](uint32(c.Capacity)),
		readBuffers: readBuffers,
		writeBuffer: queue.NewMPSC[node.WriteTask[K, V]](writeBufferCapacity),
		doneClear:   make(chan struct{}),
		mask:        uint32(readBuffersCount - 1),
		costFunc:    c.CostFunc,
		capacity:    c.Capacity,
	}

	cache.expirePolicy = expire.NewPolicy[K, V]()
	if c.StatsEnabled {
		cache.stats = stats.New()
	}

	unixtime.Start()
	go cache.process()
	go cache.cleanup()

	return cache
}

func (c *Cache[K, V]) getReadBufferIdx() int {
	return int(xruntime.Fastrand() & c.mask)
}

func (c *Cache[K, V]) Has(key K) bool {
	_, ok := c.Get(key)
	return ok
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	got, ok := c.hashmap.Get(key)
	if !ok {
		c.stats.IncMisses()
		return zeroValue[V](), false
	}

	if got.IsExpired() {
		c.writeBuffer.Insert(node.NewDeleteTask(got))
		c.stats.IncMisses()
		return zeroValue[V](), false
	}

	c.afterGet(got)
	c.stats.IncHits()

	return got.Value(), ok
}

func (c *Cache[K, V]) afterGet(got *node.Node[K, V]) {
	idx := c.getReadBufferIdx()
	pb := c.readBuffers[idx].Add(got)
	if pb != nil {
		c.evictionMutex.Lock()
		c.policy.Read(pb.Returned)
		c.evictionMutex.Unlock()

		c.readBuffers[idx].Free()
	}
}

func (c *Cache[K, V]) Set(key K, value V) {
	c.set(key, value, 0)
}

func (c *Cache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	ttl = (ttl + time.Second - 1) / time.Second
	expiration := unixtime.Now() + uint32(ttl)
	c.set(key, value, expiration)
}

func (c *Cache[K, V]) set(key K, value V, expiration uint32) {
	cost := c.costFunc(key, value)
	if cost > c.policy.MaxAvailableCost() {
		return
	}

	got, ok := c.hashmap.Get(key)
	if ok {
		if !got.IsExpired() {
			got.Lock()
			got.SetValue(value)
			costDiff := cost - got.Cost()
			if costDiff != 0 {
				got.SetCost(cost)
			}
			got.Unlock()
			if costDiff != 0 {
				c.writeBuffer.Insert(node.NewUpdateTask(got, costDiff))
			}
			return
		}

		c.writeBuffer.Insert(node.NewDeleteTask(got))
	}

	n := node.New(key, value, expiration, cost)
	evicted := c.hashmap.Set(n)
	c.writeBuffer.Insert(node.NewAddTask(n, cost))
	if evicted != nil {
		c.writeBuffer.Insert(node.NewDeleteTask(evicted))
	}
}

func (c *Cache[K, V]) Delete(key K) {
	deleted := c.hashmap.Delete(key)
	if deleted != nil {
		c.writeBuffer.Insert(node.NewDeleteTask(deleted))
	}
}

func (c *Cache[K, V]) cleanup() {
	expired := make([]*node.Node[K, V], 0, 128)
	for {
		time.Sleep(time.Second)

		c.evictionMutex.Lock()
		if c.isClosed {
			return
		}

		e := c.expirePolicy.RemoveExpired(expired)
		c.policy.Delete(e)

		c.evictionMutex.Unlock()

		for _, n := range e {
			c.hashmap.EvictNode(n)
		}

		expired = expired[:0]
	}
}

func (c *Cache[K, V]) process() {
	bufferCapacity := 128
	buffer := make([]node.WriteTask[K, V], 0, bufferCapacity)
	deleted := make([]*node.Node[K, V], 0, bufferCapacity)
	i := 0
	for {
		task := c.writeBuffer.Remove()

		if task.IsClear() || task.IsClose() {
			buffer = buffer[:0]
			c.writeBuffer.Clear()

			c.evictionMutex.Lock()
			c.policy.Clear()
			c.expirePolicy.Clear()
			if task.IsClose() {
				c.isClosed = true
			}
			c.evictionMutex.Unlock()

			c.doneClear <- struct{}{}
			if task.IsClose() {
				break
			}
			continue
		}

		buffer = append(buffer, task)
		i++
		if i >= bufferCapacity {
			i -= bufferCapacity

			c.evictionMutex.Lock()

			for _, t := range buffer {
				if t.IsDelete() {
					c.expirePolicy.Delete(t.Node())
				} else if t.IsAdd() {
					c.expirePolicy.Add(t.Node())
				}
			}

			d := c.policy.Write(deleted, buffer)
			for _, n := range d {
				c.expirePolicy.Delete(n)
			}

			c.evictionMutex.Unlock()

			for _, n := range d {
				c.hashmap.EvictNode(n)
			}

			buffer = buffer[:0]
			deleted = deleted[:0]
		}
	}
}

func (c *Cache[K, V]) Clear() {
	c.clear(node.NewClearTask[K, V]())
}

func (c *Cache[K, V]) clear(task node.WriteTask[K, V]) {
	c.hashmap.Clear()
	for i := 0; i < len(c.readBuffers); i++ {
		c.readBuffers[i].Clear()
	}

	c.writeBuffer.Insert(task)
	<-c.doneClear

	c.stats.Clear()
}

func (c *Cache[K, V]) Close() {
	c.closeOnce.Do(func() {
		c.clear(node.NewCloseTask[K, V]())
		// TODO: runtime.SetFinalizer?
		unixtime.Stop()
	})
}

func (c *Cache[K, V]) Size() int {
	return c.hashmap.Size()
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
