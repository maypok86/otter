// Copyright (c) 2023 Alexey Mayshev and contributors. All rights reserved.
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
	"context"
	"errors"
	"sync"
	"time"

	"github.com/maypok86/otter/v2/internal/clock"
	"github.com/maypok86/otter/v2/internal/deque/queue"
	"github.com/maypok86/otter/v2/internal/eviction"
	"github.com/maypok86/otter/v2/internal/eviction/s3fifo"
	"github.com/maypok86/otter/v2/internal/expiry"
	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/hashmap"
	"github.com/maypok86/otter/v2/internal/lossy"
	"github.com/maypok86/otter/v2/internal/xmath"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

const (
	minWriteBufferSize uint32 = 4
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
	Read(n node.Node[K, V])
	Add(n node.Node[K, V], nowNanos int64, evictNode func(n node.Node[K, V], nowNanos int64))
	Delete(n node.Node[K, V])
	Clear()
}

type expiryPolicy[K comparable, V any] interface {
	Add(n node.Node[K, V])
	Delete(n node.Node[K, V])
	DeleteExpired(nowNanos int64, expireNode func(n node.Node[K, V], nowNanos int64))
	Clear()
}

type timeSource interface {
	Init()
	Offset() int64
	Time(offset int64) time.Time
}

// Cache is a structure performs a best-effort bounding of a hash table using eviction algorithm
// to determine which entries to evict when the capacity is exceeded.
type Cache[K comparable, V any] struct {
	nodeManager    *node.Manager[K, V]
	hashmap        *hashmap.Map[K, V, node.Node[K, V]]
	policy         evictionPolicy[K, V]
	expiryPolicy   expiryPolicy[K, V]
	stats          statsRecorder
	logger         Logger
	clock          timeSource
	stripedBuffer  *lossy.Striped[K, V]
	writeBuffer    *queue.Growable[task[K, V]]
	singleflight   *group[K, V]
	evictionMutex  sync.Mutex
	closeOnce      sync.Once
	doneClear      chan struct{}
	doneClose      chan struct{}
	weigher        func(key K, value V) uint32
	onDeletion     func(e DeletionEvent[K, V])
	ttl            time.Duration
	withExpiration bool
	withEviction   bool
	withProcess    bool
	withStats      bool
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

	var stripedBuffer *lossy.Striped[K, V]
	if withEviction {
		stripedBuffer = lossy.NewStriped(maxStripedBufferSize, nodeManager)
	}

	var hm *hashmap.Map[K, V, node.Node[K, V]]
	if b.initialCapacity == nil {
		hm = hashmap.New[K, V, node.Node[K, V]](nodeManager)
	} else {
		hm = hashmap.NewWithSize[K, V, node.Node[K, V]](nodeManager, *b.initialCapacity)
	}

	cache := &Cache[K, V]{
		nodeManager:   nodeManager,
		hashmap:       hm,
		stats:         newStatsRecorder(b.statsRecorder),
		logger:        b.logger,
		stripedBuffer: stripedBuffer,
		singleflight:  &group[K, V]{},
		doneClear:     make(chan struct{}),
		doneClose:     make(chan struct{}, 1),
		weigher:       b.getWeigher(),
		onDeletion:    b.onDeletion,
		clock:         &clock.Real{},
	}

	if _, ok := b.statsRecorder.(noopStatsRecorder); ok {
		cache.withStats = true
	}

	cache.withEviction = withEviction
	cache.policy = eviction.NewDisabled[K, V]()
	if cache.withEviction {
		cache.policy = s3fifo.NewPolicy[K, V](*maximum)
	}

	switch {
	case b.ttl != nil:
		cache.expiryPolicy = expiry.NewFixed[K, V]()
	case b.withVariableTTL:
		cache.expiryPolicy = expiry.NewVariable[K, V](nodeManager)
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
		cache.clock.Init()
		go cache.cleanup()
	}

	if cache.withProcess {
		go cache.process()
	}

	return cache
}

func (c *Cache[K, V]) getExpiration(duration time.Duration) int64 {
	return c.clock.Offset() + duration.Nanoseconds()
}

// Has checks if there is an item with the given key in the cache.
func (c *Cache[K, V]) Has(key K) bool {
	_, ok := c.GetIfPresent(key)
	return ok
}

// GetIfPresent returns the value associated with the key in this cache.
func (c *Cache[K, V]) GetIfPresent(key K) (V, bool) {
	n := c.GetNode(key)
	if n == nil {
		return zeroValue[V](), false
	}

	return n.Value(), true
}

// GetNode returns the node associated with the key in this cache.
func (c *Cache[K, V]) GetNode(key K) node.Node[K, V] {
	n := c.hashmap.Get(key)
	if n == nil || !n.IsAlive() || n.HasExpired(c.clock.Offset()) {
		c.stats.RecordMisses(1)
		return nil
	}

	c.afterHit(n)

	return n
}

// GetNodeQuietly returns the node associated with the key in this cache.
//
// Unlike GetNode, this function does not produce any side effects
// such as updating statistics or the eviction policy.
func (c *Cache[K, V]) GetNodeQuietly(key K) node.Node[K, V] {
	n := c.hashmap.Get(key)
	if n == nil || !n.IsAlive() || n.HasExpired(c.clock.Offset()) {
		return nil
	}

	return n
}

func (c *Cache[K, V]) afterHit(got node.Node[K, V]) {
	c.stats.RecordHits(1)

	if !c.withEviction {
		return
	}

	result := c.stripedBuffer.Add(got)
	if result == lossy.Full && c.evictionMutex.TryLock() {
		c.stripedBuffer.DrainTo(c.policy.Read)
		c.evictionMutex.Unlock()
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

	var old node.Node[K, V]
	c.hashmap.Compute(key, func(current node.Node[K, V]) node.Node[K, V] {
		old = current
		if onlyIfAbsent && current != nil {
			// no op
			return current
		}
		// set
		c.singleflight.delete(key)
		return n
	})
	if onlyIfAbsent {
		if old == nil {
			c.afterWrite(n, nil)
			return value, true
		}
		return old.Value(), false
	}

	c.afterWrite(n, old)
	if old != nil {
		return old.Value(), false
	}
	return value, true
}

func (c *Cache[K, V]) afterWrite(n, old node.Node[K, V]) {
	if !c.withProcess {
		if old != nil {
			c.notifyDeletion(n.Key(), n.Value(), CauseReplacement)
		}
		return
	}

	if old == nil {
		// insert
		c.writeBuffer.Push(newAddTask(n))
		return
	}

	// update
	old.Die()
	cause := CauseReplacement
	if old.HasExpired(c.clock.Offset()) {
		cause = CauseExpiration
	}

	c.writeBuffer.Push(newUpdateTask(n, old, cause))
}

// Get returns the value associated with key in this cache, obtaining that value from loader if necessary.
// The method improves upon the conventional "if cached, return; otherwise create, cache and return" pattern.
//
// Get can return an ErrNotFound error if the Loader returns it.
// This means that the entry was not found in the data source.
//
// If another call to Get is currently loading the value for key,
// simply waits for that goroutine to finish and returns its loaded value. Note that
// multiple goroutines can concurrently load values for distinct keys.
//
// No observable state associated with this cache is modified until loading completes.
//
// WARNING: Loader.Load must not attempt to update any mappings of this cache directly.
//
// WARNING: For any given key, every loader used with it should compute the same value.
// Otherwise, a call that passes one loader may return the result of another call
// with a differently behaving loader. For example, a call that requests a short timeout
// for an RPC may wait for a similar call that requests a long timeout, or a call by an
// unprivileged user may return a resource accessible only to a privileged user making a similar call.
func (c *Cache[K, V]) Get(ctx context.Context, key K, loader Loader[K, V]) (V, error) {
	if value, ok := c.GetIfPresent(key); ok {
		return value, nil
	}

	c.singleflight.init()
	if c.withStats {
		c.clock.Init()
	}

	startTime := int64(0)

	// node.Node compute?
	cl, shouldDo := c.singleflight.startCall(ctx, key)
	if shouldDo {
		startTime = c.clock.Offset()
		c.singleflight.doCall(cl, loader, c.afterDeleteCall)
	}
	cl.wait()

	if c.withStats && shouldDo {
		loadTime := time.Duration(c.clock.Offset() - startTime)
		if cl.err == nil || errors.Is(cl.err, ErrNotFound) {
			c.stats.RecordLoadSuccess(loadTime)
		} else {
			c.stats.RecordLoadFailure(loadTime)
		}
	}

	if cl.err != nil {
		return zeroValue[V](), cl.err
	}

	return cl.value, nil
}

func (c *Cache[K, V]) afterDeleteCall(cl *call[K, V]) {
	var (
		inserted bool
		old      node.Node[K, V]
	)
	newNode := c.hashmap.Compute(cl.key, func(oldNode node.Node[K, V]) node.Node[K, V] {
		old = oldNode
		deleted := c.singleflight.deleteCall(cl)
		if !deleted || cl.err != nil {
			return oldNode
		}
		inserted = true
		return c.nodeManager.Create(cl.key, cl.value, c.defaultExpiration(), c.weigher(cl.key, cl.value))
	})
	if inserted {
		c.afterWrite(newNode, old)
	}
}

// Invalidate discards any cached value for the key.
//
// Returns previous value if any. The invalidated result reports whether the key was
// present.
func (c *Cache[K, V]) Invalidate(key K) (value V, invalidated bool) {
	var d node.Node[K, V]
	c.hashmap.Compute(key, func(n node.Node[K, V]) node.Node[K, V] {
		if n != nil {
			c.singleflight.delete(key)
			d = n
		}
		return nil
	})
	c.afterDelete(d)
	if d != nil {
		return d.Value(), true
	}
	return zeroValue[V](), false
}

func (c *Cache[K, V]) deleteNodeFromMap(n node.Node[K, V]) node.Node[K, V] {
	var deleted node.Node[K, V]
	c.hashmap.Compute(n.Key(), func(current node.Node[K, V]) node.Node[K, V] {
		if current == nil {
			return nil
		}
		if n.AsPointer() == current.AsPointer() {
			c.singleflight.delete(n.Key())
			deleted = current
			return nil
		}
		return current
	})
	return deleted
}

func (c *Cache[K, V]) deleteNode(n node.Node[K, V]) {
	c.afterDelete(c.deleteNodeFromMap(n))
}

func (c *Cache[K, V]) afterDelete(deleted node.Node[K, V]) {
	if deleted == nil {
		return
	}

	if !c.withProcess {
		c.notifyDeletion(deleted.Key(), deleted.Value(), CauseInvalidation)
		return
	}

	// delete
	deleted.Die()
	cause := CauseInvalidation
	if deleted.HasExpired(c.clock.Offset()) {
		cause = CauseExpiration
	}

	c.writeBuffer.Push(newDeleteTask(deleted, cause))
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

func (c *Cache[K, V]) cleanup() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.doneClose:
			return
		case <-ticker.C:
			c.evictionMutex.Lock()
			c.expiryPolicy.DeleteExpired(c.clock.Offset(), c.evictOrExpireNode)
			c.evictionMutex.Unlock()
		}
	}
}

func (c *Cache[K, V]) evictOrExpireNode(n node.Node[K, V], nowNanos int64) {
	c.policy.Delete(n)
	c.expiryPolicy.Delete(n)
	deleted := c.deleteNodeFromMap(n)
	if deleted != nil {
		n.Die()

		cause := CauseOverflow
		if n.HasExpired(nowNanos) {
			cause = CauseExpiration
		}

		c.notifyDeletion(n.Key(), n.Value(), cause)
		c.stats.RecordEviction(n.Weight())
	}
}

func (c *Cache[K, V]) addToPolicies(n node.Node[K, V]) {
	if !n.IsAlive() {
		return
	}

	c.expiryPolicy.Add(n)
	c.policy.Add(n, c.clock.Offset(), c.evictOrExpireNode)
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
		c.deleteFromPolicies(t.oldNode(), t.deletionCause)
		c.addToPolicies(n)
	case t.isDelete():
		c.deleteFromPolicies(n, t.deletionCause)
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
		c.stripedBuffer.Clear()
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
