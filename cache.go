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
	"math"
	"sync"
	"time"

	"github.com/maypok86/otter/v2/core"
	"github.com/maypok86/otter/v2/core/expiry"
	"github.com/maypok86/otter/v2/core/refresh"
	"github.com/maypok86/otter/v2/core/stats"
	"github.com/maypok86/otter/v2/internal/clock"
	"github.com/maypok86/otter/v2/internal/deque/queue"
	"github.com/maypok86/otter/v2/internal/eviction"
	"github.com/maypok86/otter/v2/internal/eviction/s3fifo"
	"github.com/maypok86/otter/v2/internal/expiration"
	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/hashmap"
	"github.com/maypok86/otter/v2/internal/lossy"
	"github.com/maypok86/otter/v2/internal/xmath"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

const (
	maxDuration                 = time.Duration(math.MaxInt64)
	unreachableExpiresAfter     = maxDuration
	unreachableRefreshableAfter = maxDuration
	noTime                      = int64(0)
	expireTolerance             = int64(time.Second)

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

type expirationPolicy[K comparable, V any] interface {
	Add(n node.Node[K, V])
	Delete(n node.Node[K, V])
	DeleteExpired(nowNanos int64, expireNode func(n node.Node[K, V], nowNanos int64))
	Clear()
}

type timeSource interface {
	Init()
	CachedOffset() int64
	Refresh() int64
	Offset() int64
	Nanos(offset int64) int64
	Time(offset int64) time.Time
}

func zeroValue[V any]() V {
	var v V
	return v
}

func abs(a int64) int64 {
	if a < 0 {
		return -a
	}
	return a
}

// Cache is a structure performs a best-effort bounding of a hash table using eviction algorithm
// to determine which entries to evict when the capacity is exceeded.
type Cache[K comparable, V any] struct {
	nodeManager       *node.Manager[K, V]
	hashmap           *hashmap.Map[K, V, node.Node[K, V]]
	policy            evictionPolicy[K, V]
	expirationPolicy  expirationPolicy[K, V]
	stats             stats.Recorder
	logger            Logger
	clock             timeSource
	stripedBuffer     *lossy.Striped[K, V]
	writeBuffer       *queue.Growable[task[K, V]]
	singleflight      *group[K, V]
	evictionMutex     sync.Mutex
	closeOnce         sync.Once
	doneClear         chan struct{}
	doneClose         chan struct{}
	weigher           func(key K, value V) uint32
	onDeletion        func(e DeletionEvent[K, V])
	expiryCalculator  expiry.Calculator[K, V]
	refreshCalculator refresh.Calculator[K, V]
	withTime          bool
	withExpiration    bool
	withEviction      bool
	withProcess       bool
	withStats         bool
}

// newCache returns a new cache instance based on the settings from Options.
func newCache[K comparable, V any](o *Options[K, V]) *Cache[K, V] {
	nodeManager := node.NewManager[K, V](node.Config{
		WithSize:       o.MaximumSize > 0,
		WithExpiration: o.ExpiryCalculator != nil,
		WithWeight:     o.MaximumWeight > 0,
	})

	maximum := o.getMaximum()
	withEviction := maximum > 0

	var stripedBuffer *lossy.Striped[K, V]
	if withEviction {
		stripedBuffer = lossy.NewStriped(maxStripedBufferSize, nodeManager)
	}

	var hm *hashmap.Map[K, V, node.Node[K, V]]
	if o.InitialCapacity <= 0 {
		hm = hashmap.New[K, V, node.Node[K, V]](nodeManager)
	} else {
		hm = hashmap.NewWithSize[K, V, node.Node[K, V]](nodeManager, o.InitialCapacity)
	}

	withStats := o.StatsRecorder != nil
	if !withStats {
		o.StatsRecorder = stats.NoopRecorder{}
	}

	cache := &Cache[K, V]{
		nodeManager:       nodeManager,
		hashmap:           hm,
		stats:             o.StatsRecorder,
		logger:            o.Logger,
		stripedBuffer:     stripedBuffer,
		singleflight:      &group[K, V]{},
		doneClear:         make(chan struct{}),
		doneClose:         make(chan struct{}, 1),
		weigher:           o.Weigher,
		onDeletion:        o.OnDeletion,
		clock:             &clock.Real{},
		withStats:         withStats,
		expiryCalculator:  o.ExpiryCalculator,
		refreshCalculator: o.RefreshCalculator,
	}

	cache.withEviction = withEviction
	cache.policy = eviction.NewDisabled[K, V]()
	if cache.withEviction {
		cache.policy = s3fifo.NewPolicy[K, V](maximum)
	}

	if o.ExpiryCalculator != nil {
		cache.expirationPolicy = expiration.NewVariable(nodeManager)
	} else {
		cache.expirationPolicy = expiration.NewDisabled[K, V]()
	}

	cache.withExpiration = o.ExpiryCalculator != nil
	cache.withTime = cache.withExpiration
	cache.withProcess = cache.withEviction || cache.withExpiration

	if cache.withProcess {
		cache.writeBuffer = queue.NewGrowable[task[K, V]](minWriteBufferSize, maxWriteBufferSize)
	}
	if cache.withTime {
		cache.clock.Init()
	}
	if cache.withExpiration {
		go cache.cleanup()
	}
	if cache.withProcess {
		go cache.process()
	}

	return cache
}

func (c *Cache[K, V]) nodeToEntry(n node.Node[K, V], offset int64) core.Entry[K, V] {
	var (
		nowNano   int64
		expiresAt int64
	)

	if c.withTime {
		nowNano = c.clock.Nanos(offset)
	} else {
		nowNano = noTime
	}
	if c.withExpiration {
		expiresAt = c.clock.Nanos(n.ExpiresAt())
	} else {
		expiresAt = nowNano + int64(unreachableExpiresAfter)
	}

	return core.Entry[K, V]{
		Key:           n.Key(),
		Value:         n.Value(),
		Weight:        n.Weight(),
		ExpiresAtNano: expiresAt,
		// TODO: RefreshableAtNano
		SnapshotAtNano: nowNano,
	}
}

// Has checks if there is an item with the given key in the cache.
func (c *Cache[K, V]) Has(key K) bool {
	_, ok := c.GetIfPresent(key)
	return ok
}

// GetIfPresent returns the value associated with the key in this cache.
func (c *Cache[K, V]) GetIfPresent(key K) (V, bool) {
	n := c.getNode(key)
	if n == nil {
		return zeroValue[V](), false
	}

	return n.Value(), true
}

// getNode returns the node associated with the key in this cache.
func (c *Cache[K, V]) getNode(key K) node.Node[K, V] {
	offset := c.clock.Offset()
	n := c.hashmap.Get(key)
	if n == nil || !n.IsAlive() || n.HasExpired(offset) {
		c.stats.RecordMisses(1)
		return nil
	}

	c.afterHit(n, offset)

	return n
}

// getNodeQuietly returns the node associated with the key in this cache.
//
// Unlike getNode, this function does not produce any side effects
// such as updating statistics or the eviction policy.
func (c *Cache[K, V]) getNodeQuietly(key K) node.Node[K, V] {
	offset := c.clock.Offset()
	n := c.hashmap.Get(key)
	if n == nil || !n.IsAlive() || n.HasExpired(offset) {
		return nil
	}

	return n
}

func (c *Cache[K, V]) afterHit(got node.Node[K, V], offset int64) {
	c.stats.RecordHits(1)

	c.setExpiresAtAfterRead(got, offset)

	if !c.withEviction {
		return
	}

	result := c.stripedBuffer.Add(got)
	if result == lossy.Full && c.evictionMutex.TryLock() {
		c.stripedBuffer.DrainTo(c.policy.Read)
		c.evictionMutex.Unlock()
	}
}

// Set associates the value with the key in this cache.
//
// If the specified key is not already associated with a value, then it returns new value and true.
//
// If the specified key is already associated with a value, then it returns existing value and false.
func (c *Cache[K, V]) Set(key K, value V) (V, bool) {
	return c.set(key, value, false)
}

// SetIfAbsent if the specified key is not already associated with a value associates it with the given value.
//
// If the specified key is not already associated with a value, then it returns new value and true.
//
// If the specified key is already associated with a value, then it returns existing value and false.
func (c *Cache[K, V]) SetIfAbsent(key K, value V) (V, bool) {
	return c.set(key, value, true)
}

func (c *Cache[K, V]) setExpiresAtAfterRead(n node.Node[K, V], offset int64) {
	if !c.withExpiration {
		return
	}

	entry := c.nodeToEntry(n, offset)
	currentDuration := time.Duration(n.ExpiresAt() - offset)
	expiresAfter := c.expiryCalculator.ExpireAfterRead(entry)
	diff := abs(int64(expiresAfter - currentDuration))
	if diff > 0 {
		n.CASExpiresAt(n.ExpiresAt(), offset+int64(expiresAfter))
		if diff >= expireTolerance {
			c.writeBuffer.Push(newRescheduleTask(n))
		}
	}
}

func (c *Cache[K, V]) setExpiresAtAfterWrite(n, old node.Node[K, V], offset int64) {
	if !c.withExpiration {
		return
	}

	entry := c.nodeToEntry(n, offset)
	currentDuration := unreachableExpiresAfter
	var expiresAfter time.Duration
	if old == nil {
		expiresAfter = c.expiryCalculator.ExpireAfterCreate(entry)
	} else {
		currentDuration = time.Duration(old.ExpiresAt() - offset)
		expiresAfter = c.expiryCalculator.ExpireAfterUpdate(entry, old.Value())
	}

	if currentDuration != expiresAfter {
		n.CASExpiresAt(noTime, offset+int64(expiresAfter))
	}
}

func (c *Cache[K, V]) set(key K, value V, onlyIfAbsent bool) (V, bool) {
	var (
		old node.Node[K, V]
		n   node.Node[K, V]
	)
	offset := c.clock.Offset()
	c.hashmap.Compute(key, func(current node.Node[K, V]) node.Node[K, V] {
		old = current
		if onlyIfAbsent && current != nil {
			// no op
			return current
		}
		// set
		c.singleflight.delete(key)

		n = c.nodeManager.Create(key, value, noTime, c.weigher(key, value))
		c.setExpiresAtAfterWrite(n, old, offset)
		return n
	})
	if onlyIfAbsent {
		if old == nil {
			c.afterWrite(n, nil, offset)
			return value, true
		}
		c.setExpiresAtAfterRead(old, offset)
		return old.Value(), false
	}

	c.afterWrite(n, old, offset)
	if old != nil {
		return old.Value(), false
	}
	return value, true
}

func (c *Cache[K, V]) afterWrite(n, old node.Node[K, V], offset int64) {
	if !c.withProcess {
		if old != nil {
			c.notifyDeletion(old.Key(), old.Value(), CauseReplacement)
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
	if old.HasExpired(offset) {
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

	// node.Node compute?
	loadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cl, shouldLoad := c.singleflight.startCall(key)
	if shouldLoad {
		//nolint:errcheck // there is no need to check error
		_ = c.wrapLoad(func() error {
			return c.singleflight.doCall(loadCtx, cl, loader, c.afterDeleteCall)
		})
	}
	cl.wait()

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
	offset := c.clock.Offset()
	newNode := c.hashmap.Compute(cl.key, func(oldNode node.Node[K, V]) node.Node[K, V] {
		defer cl.cancel()

		deleted := c.singleflight.deleteCall(cl)
		if cl.err != nil {
			return oldNode
		}
		if !deleted {
			if oldNode != nil {
				cl.value = oldNode.Value()
			}
			return oldNode
		}
		old = oldNode
		inserted = true
		n := c.nodeManager.Create(cl.key, cl.value, noTime, c.weigher(cl.key, cl.value))
		c.setExpiresAtAfterWrite(n, old, offset)
		return n
	})
	if inserted {
		c.afterWrite(newNode, old, offset)
	}
}

func (c *Cache[K, V]) BulkGet(ctx context.Context, keys []K, bulkLoader BulkLoader[K, V]) (map[K]V, error) {
	result := make(map[K]V, len(keys))
	var misses map[K]*call[K, V]
	for _, key := range keys {
		if _, found := result[key]; found {
			continue
		}
		if _, found := misses[key]; found {
			continue
		}
		if value, ok := c.GetIfPresent(key); ok {
			result[key] = value
			continue
		}

		if misses == nil {
			misses = make(map[K]*call[K, V], len(keys)-len(result))
		}
		misses[key] = nil
	}

	if len(misses) == 0 {
		return result, nil
	}

	loadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	c.singleflight.init()
	if c.withStats {
		c.clock.Init()
	}

	var toLoadCalls map[K]*call[K, V]
	i := 0
	for key := range misses {
		// node.Node compute?
		cl, shouldLoad := c.singleflight.startCall(key)
		if shouldLoad {
			if toLoadCalls == nil {
				toLoadCalls = make(map[K]*call[K, V], len(misses)-i)
			}

			toLoadCalls[key] = cl
		}
		misses[key] = cl
		i++
	}

	var loadErr error
	if len(toLoadCalls) > 0 {
		loadErr = c.wrapLoad(func() error {
			return c.singleflight.doBulkCall(loadCtx, toLoadCalls, bulkLoader, c.afterDeleteCall)
		})
	}
	if loadErr != nil {
		return result, loadErr
	}

	//nolint:prealloc // it's ok
	var errsFromCalls []error
	i = 0
	for key, cl := range misses {
		cl.wait()
		i++

		if cl.err == nil {
			result[key] = cl.value
			continue
		}
		if _, ok := toLoadCalls[key]; ok || errors.Is(cl.err, ErrNotFound) {
			continue
		}
		if errsFromCalls == nil {
			errsFromCalls = make([]error, 0, len(misses)-i+1)
		}
		errsFromCalls = append(errsFromCalls, cl.err)
	}

	var err error
	if len(errsFromCalls) > 0 {
		err = errors.Join(errsFromCalls...)
	}

	return result, err
}

func (c *Cache[K, V]) wrapLoad(fn func() error) error {
	startTime := c.clock.Offset()

	err := fn()

	if c.withStats {
		loadTime := time.Duration(c.clock.Offset() - startTime)
		if err == nil || errors.Is(err, ErrNotFound) {
			c.stats.RecordLoadSuccess(loadTime)
		} else {
			c.stats.RecordLoadFailure(loadTime)
		}
	}

	var pe *panicError
	if errors.As(err, &pe) {
		panic(pe)
	}

	return err
}

// Invalidate discards any cached value for the key.
//
// Returns previous value if any. The invalidated result reports whether the key was
// present.
func (c *Cache[K, V]) Invalidate(key K) (value V, invalidated bool) {
	var d node.Node[K, V]
	offset := c.clock.Offset()
	c.hashmap.Compute(key, func(n node.Node[K, V]) node.Node[K, V] {
		if n != nil {
			c.singleflight.delete(key)
			d = n
		}
		return nil
	})
	c.afterDelete(d, offset)
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

func (c *Cache[K, V]) deleteNode(n node.Node[K, V], offset int64) {
	c.afterDelete(c.deleteNodeFromMap(n), offset)
}

func (c *Cache[K, V]) afterDelete(deleted node.Node[K, V], offset int64) {
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
	if deleted.HasExpired(offset) {
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
			c.deleteNode(n, offset)
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
			offset := c.clock.Refresh()
			c.evictionMutex.Lock()
			c.expirationPolicy.DeleteExpired(offset, c.evictOrExpireNode)
			c.evictionMutex.Unlock()
		}
	}
}

func (c *Cache[K, V]) evictOrExpireNode(n node.Node[K, V], nowNanos int64) {
	c.policy.Delete(n)
	c.expirationPolicy.Delete(n)
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

func (c *Cache[K, V]) addToPolicies(n node.Node[K, V], offset int64) {
	if !n.IsAlive() {
		return
	}

	c.expirationPolicy.Add(n)
	c.policy.Add(n, offset, c.evictOrExpireNode)
}

func (c *Cache[K, V]) deleteFromPolicies(n node.Node[K, V], cause DeletionCause) {
	c.expirationPolicy.Delete(n)
	c.policy.Delete(n)
	c.notifyDeletion(n.Key(), n.Value(), cause)
}

func (c *Cache[K, V]) onWrite(t task[K, V], offset int64) {
	if t.isClear() || t.isClose() {
		c.writeBuffer.DeleteAllByPredicate(func(t task[K, V]) bool {
			return !(t.isClear() || t.isClose())
		})

		c.policy.Clear()
		c.expirationPolicy.Clear()

		if t.isClose() {
			c.doneClose <- struct{}{}
		}
		c.doneClear <- struct{}{}
		return
	}

	n := t.node()
	switch {
	case t.isAdd():
		c.addToPolicies(n, offset)
	case t.isUpdate():
		c.deleteFromPolicies(t.oldNode(), t.deletionCause)
		c.addToPolicies(n, offset)
	case t.isDelete():
		c.deleteFromPolicies(n, t.deletionCause)
	case t.isReschedule():
		c.expirationPolicy.Delete(t.n)
		c.expirationPolicy.Add(t.n)
	default:
		panic("invalid task type")
	}
}

func (c *Cache[K, V]) onBulkWrite(buffer []task[K, V], offset int64) bool {
	for _, t := range buffer {
		c.onWrite(t, offset)
		if t.isClose() {
			return true
		}
	}
	return false
}

func (c *Cache[K, V]) process() {
	const maxBufferSize = 64
	buffer := make([]task[K, V], 0, maxBufferSize)
	for {
		buffer = append(buffer, c.writeBuffer.Pop())

		for i := 0; i < maxBufferSize-1; i++ {
			t, ok := c.writeBuffer.TryPop()
			if !ok {
				break
			}
			buffer = append(buffer, t)
		}

		offset := c.clock.Offset()
		c.evictionMutex.Lock()
		shouldClose := c.onBulkWrite(buffer, offset)
		c.evictionMutex.Unlock()

		buffer = buffer[:0]

		if shouldClose {
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
