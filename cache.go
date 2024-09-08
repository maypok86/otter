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

package otter

import (
	"sync"
	"time"

	"github.com/maypok86/otter/v2/internal/clock"
	"github.com/maypok86/otter/v2/internal/eviction"
	"github.com/maypok86/otter/v2/internal/eviction/s3fifo"
	"github.com/maypok86/otter/v2/internal/expiry"
	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/hashtable"
	"github.com/maypok86/otter/v2/internal/lossy"
	"github.com/maypok86/otter/v2/internal/queue"
	"github.com/maypok86/otter/v2/internal/xmath"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

const (
	minWriteBufferSize uint32 = 4
	pinnedWeight       uint32 = 0
)

var (
	maxWriteBufferSize   uint32
	maxStripedBufferSize int
)

func init() {
	parallelism := xruntime.Parallelism()
	roundedParallelism := int(xmath.RoundUpPowerOf2(parallelism))
	//nolint:gosec // there will never be an overflow
	maxWriteBufferSize = uint32(128 * roundedParallelism)
	maxStripedBufferSize = 4 * roundedParallelism
}

type evictionPolicy[K comparable, V any] interface {
	Read(nodes []node.Node[K, V])
	Add(n node.Node[K, V], nowNanos int64)
	Delete(n node.Node[K, V])
	MaxAvailableWeight() uint64
	Clear()
}

type expiryPolicy[K comparable, V any] interface {
	Add(n node.Node[K, V])
	Delete(n node.Node[K, V])
	DeleteExpired(nowNanos int64)
	Clear()
}

// Cache is a structure performs a best-effort bounding of a hash table using eviction algorithm
// to determine which entries to evict when the capacity is exceeded.
type Cache[K comparable, V any] struct {
	nodeManager    *node.Manager[K, V]
	hashmap        *hashtable.Map[K, V]
	policy         evictionPolicy[K, V]
	expiryPolicy   expiryPolicy[K, V]
	stats          statsRecorder
	logger         Logger
	clock          *clock.Clock
	stripedBuffer  []*lossy.Buffer[K, V]
	writeBuffer    *queue.Growable[task[K, V]]
	evictionMutex  sync.Mutex
	closeOnce      sync.Once
	doneClear      chan struct{}
	doneClose      chan struct{}
	weigher        func(key K, value V) uint32
	onDeletion     func(e DeletionEvent[K, V])
	mask           uint32
	ttl            time.Duration
	withExpiration bool
	withEviction   bool
	withProcess    bool
}

// newCache returns a new cache instance based on the settings from Config.
func newCache[K comparable, V any](b *Builder[K, V]) *Cache[K, V] {
	nodeManager := node.NewManager[K, V](node.Config{
		WithSize:       b.maximumSize != nil,
		WithExpiration: b.ttl != nil || b.withVariableTTL,
		WithWeight:     b.weigher != nil,
	})

	maximum := b.getMaximum()
	withEviction := maximum != nil

	var stripedBuffer []*lossy.Buffer[K, V]
	if withEviction {
		stripedBuffer = make([]*lossy.Buffer[K, V], 0, maxStripedBufferSize)
		for i := 0; i < maxStripedBufferSize; i++ {
			stripedBuffer = append(stripedBuffer, lossy.New[K, V](nodeManager))
		}
	}

	var hashmap *hashtable.Map[K, V]
	if b.initialCapacity == nil {
		hashmap = hashtable.New[K, V](nodeManager)
	} else {
		hashmap = hashtable.NewWithSize[K, V](nodeManager, *b.initialCapacity)
	}

	cache := &Cache[K, V]{
		nodeManager:   nodeManager,
		hashmap:       hashmap,
		stats:         newStatsRecorder(b.statsRecorder),
		logger:        b.logger,
		stripedBuffer: stripedBuffer,
		doneClear:     make(chan struct{}),
		doneClose:     make(chan struct{}, 1),
		//nolint:gosec // there will never be an overflow
		mask:       uint32(maxStripedBufferSize - 1),
		weigher:    b.getWeigher(),
		onDeletion: b.onDeletion,
	}

	cache.withEviction = withEviction
	cache.policy = eviction.NewDisabled[K, V]()
	if cache.withEviction {
		cache.policy = s3fifo.NewPolicy(*maximum, cache.evictNode)
	}

	switch {
	case b.ttl != nil:
		cache.expiryPolicy = expiry.NewFixed[K, V](cache.deleteExpiredNode)
	case b.withVariableTTL:
		cache.expiryPolicy = expiry.NewVariable[K, V](nodeManager, cache.deleteExpiredNode)
	default:
		cache.expiryPolicy = expiry.NewDisabled[K, V]()
	}

	if b.ttl != nil {
		cache.ttl = *b.ttl
	}

	cache.withExpiration = b.ttl != nil || b.withVariableTTL
	cache.withProcess = cache.withEviction || cache.withExpiration

	if cache.withProcess {
		cache.writeBuffer = queue.NewGrowable[task[K, V]](minWriteBufferSize, maxWriteBufferSize)
	}

	if cache.withExpiration {
		cache.clock = clock.New()
		go cache.cleanup()
	}

	if cache.withProcess {
		go cache.process()
	}

	return cache
}

func (c *Cache[K, V]) getReadBufferIdx() int {
	return int(xruntime.Fastrand() & c.mask)
}

func (c *Cache[K, V]) getExpiration(duration time.Duration) int64 {
	return c.clock.Offset() + duration.Nanoseconds()
}

// Has checks if there is an item with the given key in the cache.
func (c *Cache[K, V]) Has(key K) bool {
	_, ok := c.Get(key)
	return ok
}

// Get returns the value associated with the key in this cache.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	n, ok := c.GetNode(key)
	if !ok {
		return zeroValue[V](), false
	}

	return n.Value(), true
}

// GetNode returns the node associated with the key in this cache.
func (c *Cache[K, V]) GetNode(key K) (node.Node[K, V], bool) {
	n, ok := c.hashmap.Get(key)
	if !ok || !n.IsAlive() {
		c.stats.RecordMisses(1)
		return nil, false
	}

	if n.HasExpired(c.clock.Offset()) {
		// withProcess = true
		// avoid duplicate push
		deleted := c.hashmap.DeleteNode(n)
		if deleted != nil {
			n.Die()
			c.writeBuffer.Push(newExpiredTask(n))
		}
		c.stats.RecordMisses(1)
		return nil, false
	}

	c.afterGet(n)
	c.stats.RecordHits(1)

	return n, true
}

// GetNodeQuietly returns the node associated with the key in this cache.
//
// Unlike GetNode, this function does not produce any side effects
// such as updating statistics or the eviction policy.
func (c *Cache[K, V]) GetNodeQuietly(key K) (node.Node[K, V], bool) {
	n, ok := c.hashmap.Get(key)
	if !ok || !n.IsAlive() || n.HasExpired(c.clock.Offset()) {
		return nil, false
	}

	return n, true
}

func (c *Cache[K, V]) afterGet(got node.Node[K, V]) {
	if !c.withEviction {
		return
	}

	idx := c.getReadBufferIdx()
	pb := c.stripedBuffer[idx].Add(got)
	if pb != nil {
		c.evictionMutex.Lock()
		c.policy.Read(pb.Returned)
		c.evictionMutex.Unlock()

		c.stripedBuffer[idx].Free()
	}
}

func (c *Cache[K, V]) defaultExpiration() int64 {
	if c.ttl == 0 {
		return 0
	}

	return c.getExpiration(c.ttl)
}

// Set associates the value with the key in this cache.
//
// If the specified key is not already associated with a value, then it returns new value and true.
//
// If the specified key is already associated with a value, then it returns existing value and false.
func (c *Cache[K, V]) Set(key K, value V) (V, bool) {
	return c.set(key, value, c.defaultExpiration(), false)
}

// SetWithTTL associates the value with the key in this cache and sets the custom ttl for this key-value item.
//
// If the specified key is not already associated with a value, then it returns new value and true.
//
// If the specified key is already associated with a value, then it returns existing value and false.
func (c *Cache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) (V, bool) {
	return c.set(key, value, c.getExpiration(ttl), false)
}

// SetIfAbsent if the specified key is not already associated with a value associates it with the given value.
//
// If the specified key is not already associated with a value, then it returns new value and true.
//
// If the specified key is already associated with a value, then it returns existing value and false.
func (c *Cache[K, V]) SetIfAbsent(key K, value V) (V, bool) {
	return c.set(key, value, c.defaultExpiration(), true)
}

// SetIfAbsentWithTTL if the specified key is not already associated with a value associates it with the given value
// and sets the custom ttl for this key-value item.
//
// If the specified key is not already associated with a value, then it returns new value and true.
//
// If the specified key is already associated with a value, then it returns existing value and false.
func (c *Cache[K, V]) SetIfAbsentWithTTL(key K, value V, ttl time.Duration) (V, bool) {
	return c.set(key, value, c.getExpiration(ttl), true)
}

func (c *Cache[K, V]) set(key K, value V, expiration int64, onlyIfAbsent bool) (V, bool) {
	n := c.nodeManager.Create(key, value, expiration, c.weigher(key, value))
	if onlyIfAbsent {
		res := c.hashmap.SetIfAbsent(n)
		if res == nil {
			c.afterWrite(n, nil)
			return value, true
		}
		return res.Value(), false
	}

	evicted := c.hashmap.Set(n)
	c.afterWrite(n, evicted)

	if evicted != nil {
		return evicted.Value(), false
	}
	return value, true
}

func (c *Cache[K, V]) afterWrite(n, evicted node.Node[K, V]) {
	if !c.withProcess {
		if evicted != nil {
			c.notifyDeletion(n.Key(), n.Value(), CauseReplacement)
		}
		return
	}

	if evicted != nil {
		// update
		evicted.Die()
		c.writeBuffer.Push(newUpdateTask(n, evicted))
	} else {
		// insert
		c.writeBuffer.Push(newAddTask(n))
	}
}

// Delete deletes the association for this key from the cache.
//
// Returns previous value if any. The deleted result reports whether the key was
// present.
func (c *Cache[K, V]) Delete(key K) (value V, deleted bool) {
	d := c.hashmap.Delete(key)
	c.afterDelete(d)
	if d != nil {
		return d.Value(), true
	}
	return zeroValue[V](), false
}

func (c *Cache[K, V]) deleteNode(n node.Node[K, V]) {
	c.afterDelete(c.hashmap.DeleteNode(n))
}

func (c *Cache[K, V]) afterDelete(deleted node.Node[K, V]) {
	if deleted == nil {
		return
	}

	if !c.withProcess {
		c.notifyDeletion(deleted.Key(), deleted.Value(), CauseInvalidation)
		return
	}

	deleted.Die()
	c.writeBuffer.Push(newDeleteTask(deleted))
}

// InvalidateByFunc deletes the association for this key from the cache when the given function returns true.
func (c *Cache[K, V]) InvalidateByFunc(fn func(key K, value V) bool) {
	offset := c.clock.Offset()
	c.hashmap.Range(func(n node.Node[K, V]) bool {
		if !n.IsAlive() || n.HasExpired(offset) {
			return true
		}

		if fn(n.Key(), n.Value()) {
			c.deleteNode(n)
		}

		return true
	})
}

func (c *Cache[K, V]) notifyDeletion(key K, value V, cause DeletionCause) {
	if c.onDeletion == nil {
		return
	}

	c.onDeletion(DeletionEvent[K, V]{
		Key:   key,
		Value: value,
		Cause: cause,
	})
}

func (c *Cache[K, V]) deleteExpiredNode(n node.Node[K, V]) {
	c.policy.Delete(n)
	deleted := c.hashmap.DeleteNode(n)
	if deleted != nil {
		n.Die()
		c.notifyDeletion(n.Key(), n.Value(), CauseExpiration)
		c.stats.RecordEviction(n.Weight())
	}
}

func (c *Cache[K, V]) cleanup() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.doneClose:
			return
		case <-ticker.C:
			c.evictionMutex.Lock()
			c.expiryPolicy.DeleteExpired(c.clock.Offset())
			c.evictionMutex.Unlock()
		}
	}
}

func (c *Cache[K, V]) evictNode(n node.Node[K, V]) {
	c.expiryPolicy.Delete(n)
	deleted := c.hashmap.DeleteNode(n)
	if deleted != nil {
		n.Die()
		c.notifyDeletion(n.Key(), n.Value(), CauseOverflow)
		c.stats.RecordEviction(n.Weight())
	}
}

func (c *Cache[K, V]) addToPolicies(n node.Node[K, V]) {
	if !n.IsAlive() {
		return
	}

	if uint64(n.Weight()) > c.policy.MaxAvailableWeight() {
		deleted := c.hashmap.DeleteNode(n)
		if deleted != nil {
			n.Die()
		}
		c.stats.RecordRejections(1)
		return
	}

	c.expiryPolicy.Add(n)
	if n.Weight() != pinnedWeight {
		c.policy.Add(n, c.clock.Offset())
	}
}

func (c *Cache[K, V]) deleteFromPolicies(n node.Node[K, V], cause DeletionCause) {
	c.expiryPolicy.Delete(n)
	c.policy.Delete(n)
	c.notifyDeletion(n.Key(), n.Value(), cause)
}

func (c *Cache[K, V]) onWrite(t task[K, V]) {
	if t.isClear() || t.isClose() {
		c.writeBuffer.Clear()

		c.policy.Clear()
		c.expiryPolicy.Clear()

		if t.isClose() {
			c.doneClose <- struct{}{}
		}
		c.doneClear <- struct{}{}
		return
	}

	n := t.node()
	switch {
	case t.isAdd():
		c.addToPolicies(n)
	case t.isUpdate():
		c.deleteFromPolicies(t.oldNode(), CauseReplacement)
		c.addToPolicies(n)
	case t.isDelete():
		c.deleteFromPolicies(n, CauseInvalidation)
	case t.isExpired():
		c.deleteFromPolicies(n, CauseExpiration)
	default:
		panic("invalid task type")
	}
}

func (c *Cache[K, V]) process() {
	for {
		t := c.writeBuffer.Pop()

		c.evictionMutex.Lock()
		c.onWrite(t)
		c.evictionMutex.Unlock()

		if t.isClose() {
			break
		}
	}
}

// Range iterates over all items in the cache.
//
// Iteration stops early when the given function returns false.
func (c *Cache[K, V]) Range(fn func(key K, value V) bool) {
	offset := c.clock.Offset()
	c.hashmap.Range(func(n node.Node[K, V]) bool {
		if !n.IsAlive() || n.HasExpired(offset) {
			return true
		}

		return fn(n.Key(), n.Value())
	})
}

// InvalidateAll discards all entries in the cache.
//
// NOTE: this operation must be performed when no requests are made to the cache otherwise the behavior is undefined.
func (c *Cache[K, V]) InvalidateAll() {
	c.clear(newClearTask[K, V]())
}

func (c *Cache[K, V]) clear(t task[K, V]) {
	c.hashmap.Clear()

	if !c.withProcess {
		return
	}

	if c.withEviction {
		for i := 0; i < len(c.stripedBuffer); i++ {
			c.stripedBuffer[i].Clear()
		}
	}

	c.writeBuffer.Push(t)
	<-c.doneClear
}

// Close discards all entries in the cache and stop all goroutines.
//
// NOTE: this operation must be performed when no requests are made to the cache otherwise the behavior is undefined.
func (c *Cache[K, V]) Close() {
	c.closeOnce.Do(func() {
		c.clear(newCloseTask[K, V]())
	})
}

// Size returns the current number of items in the cache.
func (c *Cache[K, V]) Size() int {
	return c.hashmap.Size()
}

// Extension returns access to inspect and perform low-level operations on this cache based on its runtime
// characteristics. These operations are optional and dependent on how the cache was constructed
// and what abilities the implementation exposes.
func (c *Cache[K, V]) Extension() Extension[K, V] {
	return newExtension(c)
}
