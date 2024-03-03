// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package core

import (
	"sync"
	"time"

	"github.com/maypok86/otter/internal/expire"
	"github.com/maypok86/otter/internal/generated/node"
	"github.com/maypok86/otter/internal/hashtable"
	"github.com/maypok86/otter/internal/lossy"
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

func getExpiration(ttl time.Duration) uint32 {
	ttlSecond := (ttl + time.Second - 1) / time.Second
	return unixtime.Now() + uint32(ttlSecond)
}

// Config is a set of cache settings.
type Config[K comparable, V any] struct {
	Capacity        int
	InitialCapacity *int
	StatsEnabled    bool
	TTL             *time.Duration
	WithVariableTTL bool
	CostFunc        func(key K, value V) uint32
	WithCost        bool
}

type expirePolicy[K comparable, V any] interface {
	Add(n node.Node[K, V])
	Delete(n node.Node[K, V])
	RemoveExpired(expired []node.Node[K, V]) []node.Node[K, V]
	Clear()
}

// Cache is a structure performs a best-effort bounding of a hash table using eviction algorithm
// to determine which entries to evict when the capacity is exceeded.
type Cache[K comparable, V any] struct {
	nodeManager    *node.Manager[K, V]
	hashmap        *hashtable.Map[K, V]
	policy         *s3fifo.Policy[K, V]
	expirePolicy   expirePolicy[K, V]
	stats          *stats.Stats
	readBuffers    []*lossy.Buffer[K, V]
	writeBuffer    *queue.MPSC[task[K, V]]
	evictionMutex  sync.Mutex
	closeOnce      sync.Once
	doneClear      chan struct{}
	costFunc       func(key K, value V) uint32
	capacity       int
	mask           uint32
	ttl            uint32
	withExpiration bool
	isClosed       bool
}

// NewCache returns a new cache instance based on the settings from Config.
func NewCache[K comparable, V any](c Config[K, V]) *Cache[K, V] {
	parallelism := xruntime.Parallelism()
	roundedParallelism := int(xmath.RoundUpPowerOf2(parallelism))
	writeBufferCapacity := 128 * roundedParallelism
	readBuffersCount := 4 * roundedParallelism

	nodeManager := node.NewManager[K, V](node.Config{
		WithExpiration: c.TTL != nil || c.WithVariableTTL,
		WithCost:       c.WithCost,
	})

	readBuffers := make([]*lossy.Buffer[K, V], 0, readBuffersCount)
	for i := 0; i < readBuffersCount; i++ {
		readBuffers = append(readBuffers, lossy.New[K, V](nodeManager))
	}

	var hashmap *hashtable.Map[K, V]
	if c.InitialCapacity == nil {
		hashmap = hashtable.New[K, V](nodeManager)
	} else {
		hashmap = hashtable.NewWithSize[K, V](nodeManager, *c.InitialCapacity)
	}

	var expPolicy expirePolicy[K, V]
	switch {
	case c.TTL != nil:
		expPolicy = expire.NewFixed[K, V]()
	case c.WithVariableTTL:
		expPolicy = expire.NewVariable[K, V](nodeManager)
	default:
		expPolicy = expire.NewDisabled[K, V]()
	}

	cache := &Cache[K, V]{
		nodeManager:  nodeManager,
		hashmap:      hashmap,
		policy:       s3fifo.NewPolicy[K, V](uint32(c.Capacity)),
		expirePolicy: expPolicy,
		readBuffers:  readBuffers,
		writeBuffer:  queue.NewMPSC[task[K, V]](writeBufferCapacity),
		doneClear:    make(chan struct{}),
		mask:         uint32(readBuffersCount - 1),
		costFunc:     c.CostFunc,
		capacity:     c.Capacity,
	}

	if c.StatsEnabled {
		cache.stats = stats.New()
	}
	if c.TTL != nil {
		cache.ttl = uint32((*c.TTL + time.Second - 1) / time.Second)
	}

	cache.withExpiration = c.TTL != nil || c.WithVariableTTL

	if cache.withExpiration {
		unixtime.Start()
		go cache.cleanup()
	}

	go cache.process()

	return cache
}

func (c *Cache[K, V]) getReadBufferIdx() int {
	return int(xruntime.Fastrand() & c.mask)
}

// Has checks if there is an item with the given key in the cache.
func (c *Cache[K, V]) Has(key K) bool {
	_, ok := c.Get(key)
	return ok
}

// Get returns the value associated with the key in this cache.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	got, ok := c.hashmap.Get(key)
	if !ok || !got.IsAlive() {
		c.stats.IncMisses()
		return zeroValue[V](), false
	}

	if got.IsExpired() {
		c.writeBuffer.Insert(newDeleteTask(got))
		c.stats.IncMisses()
		return zeroValue[V](), false
	}

	c.afterGet(got)
	c.stats.IncHits()

	return got.Value(), ok
}

func (c *Cache[K, V]) afterGet(got node.Node[K, V]) {
	idx := c.getReadBufferIdx()
	pb := c.readBuffers[idx].Add(got)
	if pb != nil {
		c.evictionMutex.Lock()
		c.policy.Read(pb.Returned)
		c.evictionMutex.Unlock()

		c.readBuffers[idx].Free()
	}
}

// Set associates the value with the key in this cache.
//
// If it returns false, then the key-value item had too much cost and the Set was dropped.
func (c *Cache[K, V]) Set(key K, value V) bool {
	return c.set(key, value, c.defaultExpiration(), false)
}

func (c *Cache[K, V]) defaultExpiration() uint32 {
	if c.ttl == 0 {
		return 0
	}

	return unixtime.Now() + c.ttl
}

// SetWithTTL associates the value with the key in this cache and sets the custom ttl for this key-value item.
//
// If it returns false, then the key-value item had too much cost and the SetWithTTL was dropped.
func (c *Cache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) bool {
	return c.set(key, value, getExpiration(ttl), false)
}

// SetIfAbsent if the specified key is not already associated with a value associates it with the given value.
//
// If the specified key is not already associated with a value, then it returns false.
//
// Also, it returns false if the key-value item had too much cost and the SetIfAbsent was dropped.
func (c *Cache[K, V]) SetIfAbsent(key K, value V) bool {
	return c.set(key, value, c.defaultExpiration(), true)
}

// SetIfAbsentWithTTL if the specified key is not already associated with a value associates it with the given value
// and sets the custom ttl for this key-value item.
//
// If the specified key is not already associated with a value, then it returns false.
//
// Also, it returns false if the key-value item had too much cost and the SetIfAbsent was dropped.
func (c *Cache[K, V]) SetIfAbsentWithTTL(key K, value V, ttl time.Duration) bool {
	return c.set(key, value, getExpiration(ttl), true)
}

func (c *Cache[K, V]) set(key K, value V, expiration uint32, onlyIfAbsent bool) bool {
	cost := c.costFunc(key, value)
	if cost > c.policy.MaxAvailableCost() {
		c.stats.IncRejectedSets()
		return false
	}

	n := c.nodeManager.Create(key, value, expiration, cost)
	if onlyIfAbsent {
		res := c.hashmap.SetIfAbsent(n)
		if res == nil {
			// insert
			c.writeBuffer.Insert(newAddTask(n))
			return true
		}
		c.stats.IncRejectedSets()
		return false
	}

	evicted := c.hashmap.Set(n)
	if evicted != nil {
		// update
		evicted.Die()
		c.writeBuffer.Insert(newUpdateTask(n, evicted))
	} else {
		// insert
		c.writeBuffer.Insert(newAddTask(n))
	}

	return true
}

// Delete removes the association for this key from the cache.
func (c *Cache[K, V]) Delete(key K) {
	c.afterDelete(c.hashmap.Delete(key))
}

func (c *Cache[K, V]) deleteNode(n node.Node[K, V]) {
	c.afterDelete(c.hashmap.DeleteNode(n))
}

func (c *Cache[K, V]) afterDelete(deleted node.Node[K, V]) {
	if deleted != nil {
		deleted.Die()
		c.writeBuffer.Insert(newDeleteTask(deleted))
	}
}

// DeleteByFunc removes the association for this key from the cache when the given function returns true.
func (c *Cache[K, V]) DeleteByFunc(f func(key K, value V) bool) {
	c.hashmap.Range(func(n node.Node[K, V]) bool {
		if !n.IsAlive() || n.IsExpired() {
			return true
		}

		if f(n.Key(), n.Value()) {
			c.deleteNode(n)
		}

		return true
	})
}

func (c *Cache[K, V]) cleanup() {
	bufferCapacity := 64
	expired := make([]node.Node[K, V], 0, bufferCapacity)
	for {
		time.Sleep(time.Second)

		c.evictionMutex.Lock()
		if c.isClosed {
			return
		}

		expired = c.expirePolicy.RemoveExpired(expired)
		for _, n := range expired {
			c.policy.Delete(n)
		}

		c.evictionMutex.Unlock()

		for _, n := range expired {
			c.hashmap.DeleteNode(n)
			n.Die()
		}

		expired = clearBuffer(expired)
		if cap(expired) > 3*bufferCapacity {
			expired = make([]node.Node[K, V], 0, bufferCapacity)
		}
	}
}

func (c *Cache[K, V]) process() {
	bufferCapacity := 64
	buffer := make([]task[K, V], 0, bufferCapacity)
	deleted := make([]node.Node[K, V], 0, bufferCapacity)
	i := 0
	for {
		t := c.writeBuffer.Remove()

		if t.isClear() || t.isClose() {
			buffer = clearBuffer(buffer)
			c.writeBuffer.Clear()

			c.evictionMutex.Lock()
			c.policy.Clear()
			c.expirePolicy.Clear()
			if t.isClose() {
				c.isClosed = true
			}
			c.evictionMutex.Unlock()

			c.doneClear <- struct{}{}
			if t.isClose() {
				break
			}
			continue
		}

		buffer = append(buffer, t)
		i++
		if i >= bufferCapacity {
			i -= bufferCapacity

			c.evictionMutex.Lock()

			for _, t := range buffer {
				n := t.node()
				switch {
				case t.isDelete():
					c.expirePolicy.Delete(n)
					c.policy.Delete(n)
				case t.isAdd():
					if n.IsAlive() {
						c.expirePolicy.Add(n)
						deleted = c.policy.Add(deleted, n)
					}
				case t.isUpdate():
					oldNode := t.oldNode()
					c.expirePolicy.Delete(oldNode)
					c.policy.Delete(oldNode)
					if n.IsAlive() {
						c.expirePolicy.Add(n)
						deleted = c.policy.Add(deleted, n)
					}
				}
			}

			for _, n := range deleted {
				c.expirePolicy.Delete(n)
			}

			c.evictionMutex.Unlock()

			for _, n := range deleted {
				c.hashmap.DeleteNode(n)
				n.Die()
				c.stats.IncEvictedCount()
				c.stats.AddEvictedCost(n.Cost())
			}

			buffer = clearBuffer(buffer)
			deleted = clearBuffer(deleted)
			if cap(deleted) > 3*bufferCapacity {
				deleted = make([]node.Node[K, V], 0, bufferCapacity)
			}
		}
	}
}

// Range iterates over all items in the cache.
//
// Iteration stops early when the given function returns false.
func (c *Cache[K, V]) Range(f func(key K, value V) bool) {
	c.hashmap.Range(func(n node.Node[K, V]) bool {
		if !n.IsAlive() || n.IsExpired() {
			return true
		}

		return f(n.Key(), n.Value())
	})
}

// Clear clears the hash table, all policies, buffers, etc.
//
// NOTE: this operation must be performed when no requests are made to the cache otherwise the behavior is undefined.
func (c *Cache[K, V]) Clear() {
	c.clear(newClearTask[K, V]())
}

func (c *Cache[K, V]) clear(t task[K, V]) {
	c.hashmap.Clear()
	for i := 0; i < len(c.readBuffers); i++ {
		c.readBuffers[i].Clear()
	}

	c.writeBuffer.Insert(t)
	<-c.doneClear

	c.stats.Clear()
}

// Close clears the hash table, all policies, buffers, etc and stop all goroutines.
//
// NOTE: this operation must be performed when no requests are made to the cache otherwise the behavior is undefined.
func (c *Cache[K, V]) Close() {
	c.closeOnce.Do(func() {
		c.clear(newCloseTask[K, V]())
		if c.withExpiration {
			unixtime.Stop()
		}
	})
}

// Size returns the current number of items in the cache.
func (c *Cache[K, V]) Size() int {
	return c.hashmap.Size()
}

// Capacity returns the cache capacity.
func (c *Cache[K, V]) Capacity() int {
	return c.capacity
}

// Stats returns a current snapshot of this cache's cumulative statistics.
func (c *Cache[K, V]) Stats() *stats.Stats {
	return c.stats
}

func clearBuffer[T any](buffer []T) []T {
	var zero T
	for i := 0; i < len(buffer); i++ {
		buffer[i] = zero
	}
	return buffer[:0]
}
