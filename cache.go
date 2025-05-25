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
	"iter"
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
	unreachableExpiresAfter     = xruntime.MaxDuration
	unreachableRefreshableAfter = xruntime.MaxDuration
	expireTolerance             = int64(time.Second)
	noTime                      = int64(0)

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
	doneClose         chan struct{}
	weigher           func(key K, value V) uint32
	onDeletion        func(e DeletionEvent[K, V])
	expiryCalculator  expiry.Calculator[K, V]
	refreshCalculator refresh.Calculator[K, V]
	withTime          bool
	withExpiration    bool
	withRefresh       bool
	withEviction      bool
	withProcess       bool
	withStats         bool
}

// newCache returns a new cache instance based on the settings from Options.
func newCache[K comparable, V any](o *Options[K, V]) *Cache[K, V] {
	nodeManager := node.NewManager[K, V](node.Config{
		WithSize:       o.MaximumSize > 0,
		WithExpiration: o.ExpiryCalculator != nil,
		WithRefresh:    o.RefreshCalculator != nil,
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
	cache.withRefresh = o.RefreshCalculator != nil
	cache.withTime = cache.withExpiration || cache.withRefresh
	cache.withProcess = cache.withEviction || cache.withExpiration

	if cache.withProcess {
		cache.doneClose = make(chan struct{})
		cache.writeBuffer = queue.NewGrowable[task[K, V]](minWriteBufferSize, maxWriteBufferSize)
	}
	if cache.withTime {
		cache.clock.Init()
	}
	if cache.withExpiration {
		go cache.periodicExpire()
	}
	if cache.withProcess {
		go cache.process()
	}

	return cache
}

func (c *Cache[K, V]) newNode(key K, value V, old node.Node[K, V]) node.Node[K, V] {
	weight := c.weigher(key, value)
	expiresAt := int64(unreachableExpiresAfter)
	if c.withExpiration && old != nil {
		expiresAt = old.ExpiresAt()
	}
	refreshableAt := int64(unreachableRefreshableAfter)
	if c.withRefresh && old != nil {
		refreshableAt = old.RefreshableAt()
	}
	return c.nodeManager.Create(key, value, expiresAt, refreshableAt, weight)
}

func (c *Cache[K, V]) nodeToEntry(n node.Node[K, V], offset int64) core.Entry[K, V] {
	nowNano := noTime
	if c.withTime {
		nowNano = c.clock.Nanos(offset)
	}

	expiresAt := int64(unreachableExpiresAfter)
	if c.withExpiration {
		expiresAt = c.clock.Nanos(n.ExpiresAt())
	}

	refreshableAt := int64(unreachableRefreshableAfter)
	if c.withRefresh {
		refreshableAt = c.clock.Nanos(n.RefreshableAt())
	}

	return core.Entry[K, V]{
		Key:               n.Key(),
		Value:             n.Value(),
		Weight:            n.Weight(),
		ExpiresAtNano:     expiresAt,
		RefreshableAtNano: refreshableAt,
		SnapshotAtNano:    nowNano,
	}
}

// Has checks if there is an item with the given key in the cache.
func (c *Cache[K, V]) Has(key K) bool {
	_, ok := c.GetIfPresent(key)
	return ok
}

// GetIfPresent returns the value associated with the key in this cache.
func (c *Cache[K, V]) GetIfPresent(key K) (V, bool) {
	offset := c.clock.Offset()
	n := c.getNode(key, offset)
	if n == nil {
		return zeroValue[V](), false
	}

	return n.Value(), true
}

// getNode returns the node associated with the key in this cache.
func (c *Cache[K, V]) getNode(key K, offset int64) node.Node[K, V] {
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
func (c *Cache[K, V]) getNodeQuietly(key K, offset int64) node.Node[K, V] {
	n := c.hashmap.Get(key)
	if n == nil || !n.IsAlive() || n.HasExpired(offset) {
		return nil
	}

	return n
}

func (c *Cache[K, V]) afterHit(got node.Node[K, V], offset int64) {
	c.stats.RecordHits(1)

	c.calcExpiresAtAfterRead(got, offset)

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

func (c *Cache[K, V]) calcExpiresAtAfterRead(n node.Node[K, V], offset int64) {
	if !c.withExpiration {
		return
	}

	expiresAfter := c.expiryCalculator.ExpireAfterRead(c.nodeToEntry(n, offset))
	c.setExpiresAfterRead(n, offset, expiresAfter, false)
}

func (c *Cache[K, V]) setExpiresAfterRead(n node.Node[K, V], offset int64, expiresAfter time.Duration, isManual bool) {
	if expiresAfter <= 0 {
		return
	}

	expiresAt := n.ExpiresAt()
	currentDuration := time.Duration(expiresAt - offset)
	diff := xmath.Abs(int64(expiresAfter - currentDuration))
	if diff > 0 {
		n.CASExpiresAt(expiresAt, offset+int64(expiresAfter))
		c.writeBuffer.Push(newRescheduleTask(n))
		if isManual || diff >= expireTolerance {
			c.writeBuffer.Push(newRescheduleTask(n))
		}
	}
}

// GetEntry returns the cache entry associated with the key in this cache.
func (c *Cache[K, V]) GetEntry(key K) (core.Entry[K, V], bool) {
	offset := c.clock.Offset()
	n := c.getNode(key, offset)
	if n == nil {
		return core.Entry[K, V]{}, false
	}
	return c.nodeToEntry(n, offset), true
}

// GetEntryQuietly returns the cache entry associated with the key in this cache.
//
// Unlike GetEntry, this function does not produce any side effects
// such as updating statistics or the eviction policy.
func (c *Cache[K, V]) GetEntryQuietly(key K) (core.Entry[K, V], bool) {
	offset := c.clock.Offset()
	n := c.getNodeQuietly(key, offset)
	if n == nil {
		return core.Entry[K, V]{}, false
	}
	return c.nodeToEntry(n, offset), true
}

// SetExpiresAfter specifies that the entry should be automatically removed from the cache once the duration has
// elapsed. The expiration policy determines when the entry's age is reset.
func (c *Cache[K, V]) SetExpiresAfter(key K, expiresAfter time.Duration) {
	if !c.withExpiration || expiresAfter <= 0 {
		return
	}

	offset := c.clock.Offset()
	n := c.hashmap.Get(key)
	if n == nil {
		return
	}

	c.setExpiresAfterRead(n, offset, expiresAfter, true)
}

// SetRefreshableAfter specifies that each entry should be eligible for reloading once a fixed duration has elapsed.
// The refresh policy determines when the entry's age is reset.
func (c *Cache[K, V]) SetRefreshableAfter(key K, refreshableAfter time.Duration) {
	if !c.withRefresh || refreshableAfter <= 0 {
		return
	}

	offset := c.clock.Offset()
	n := c.hashmap.Get(key)
	if n == nil {
		return
	}

	entry := c.nodeToEntry(n, offset)
	currentDuration := entry.RefreshableAfter()
	if refreshableAfter > 0 && currentDuration != refreshableAfter {
		n.SetRefreshableAt(offset + int64(refreshableAfter))
	}
}

func (c *Cache[K, V]) calcExpiresAtAfterWrite(n, old node.Node[K, V], offset int64) {
	if !c.withExpiration {
		return
	}

	entry := c.nodeToEntry(n, offset)
	currentDuration := entry.ExpiresAfter()
	var expiresAfter time.Duration
	if old == nil {
		expiresAfter = c.expiryCalculator.ExpireAfterCreate(entry)
	} else {
		expiresAfter = c.expiryCalculator.ExpireAfterUpdate(entry, old.Value())
	}

	if expiresAfter > 0 && currentDuration != expiresAfter {
		n.SetExpiresAt(offset + int64(expiresAfter))
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
		n = c.newNode(key, value, old)
		c.calcExpiresAtAfterWrite(n, old, offset)
		c.calcRefreshableAt(n, old, nil, offset)
		return n
	})
	if onlyIfAbsent {
		if old == nil {
			c.afterWrite(n, nil, offset)
			return value, true
		}
		c.calcExpiresAtAfterRead(old, offset)
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

type refreshableKey[K comparable] struct {
	key   K
	found bool
}

func (c *Cache[K, V]) refreshKey(rk refreshableKey[K], loader Loader[K, V]) {
	if !c.withRefresh {
		return
	}

	go func() {
		var refresher func(ctx context.Context, key K) (V, error)
		if rk.found {
			refresher = loader.Reload
		} else {
			refresher = loader.Load
		}

		loadCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		cl, shouldLoad := c.singleflight.startCall(rk.key, true)
		if shouldLoad {
			//nolint:errcheck // there is no need to check error
			_ = c.wrapLoad(func() error {
				return c.singleflight.doCall(loadCtx, cl, refresher, c.afterDeleteCall)
			})
		}
		cl.wait()

		if cl.err != nil && !cl.isNotFound {
			c.logger.Error(loadCtx, "Returned an error during the refreshing", cl.err)
		}
	}()
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
	c.singleflight.init()

	offset := c.clock.Offset()
	n := c.getNode(key, offset)
	if n != nil {
		if !n.IsFresh(offset) {
			c.refreshKey(refreshableKey[K]{
				key:   n.Key(),
				found: true,
			}, loader)
		}
		return n.Value(), nil
	}

	if c.withStats {
		c.clock.Init()
	}

	// node.Node compute?
	loadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cl, shouldLoad := c.singleflight.startCall(key, false)
	if shouldLoad {
		//nolint:errcheck // there is no need to check error
		_ = c.wrapLoad(func() error {
			return c.singleflight.doCall(loadCtx, cl, loader.Load, c.afterDeleteCall)
		})
	}
	cl.wait()

	if cl.err != nil {
		return zeroValue[V](), cl.err
	}

	return cl.value, nil
}

func (c *Cache[K, V]) calcRefreshableAt(n, old node.Node[K, V], cl *call[K, V], offset int64) {
	if !c.withRefresh {
		return
	}

	var refreshableAfter time.Duration
	entry := c.nodeToEntry(n, offset)
	currentDuration := entry.RefreshableAfter()
	//nolint:gocritic // it's ok
	if cl != nil && cl.isRefresh && old != nil {
		if cl.err != nil {
			if !cl.isNotFound {
				refreshableAfter = c.refreshCalculator.RefreshAfterReloadFailure(entry, cl.err)
			}
		} else {
			refreshableAfter = c.refreshCalculator.RefreshAfterReload(entry, old.Value())
		}
	} else if old != nil {
		refreshableAfter = c.refreshCalculator.RefreshAfterUpdate(entry, old.Value())
	} else {
		refreshableAfter = c.refreshCalculator.RefreshAfterCreate(entry)
	}

	if refreshableAfter > 0 && currentDuration != refreshableAfter {
		n.SetRefreshableAt(offset + int64(refreshableAfter))
	}
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
			c.calcRefreshableAt(oldNode, oldNode, cl, offset)
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
		n := c.newNode(cl.key, cl.value, old)
		c.calcExpiresAtAfterWrite(n, old, offset)
		c.calcRefreshableAt(n, old, cl, offset)
		return n
	})
	if inserted {
		c.afterWrite(newNode, old, offset)
	}
}

func (c *Cache[K, V]) bulkRefreshKeys(rks []refreshableKey[K], bulkLoader BulkLoader[K, V]) {
	if !c.withRefresh || len(rks) == 0 {
		return
	}

	go func() {
		var (
			toLoadCalls   map[K]*call[K, V]
			toReloadCalls map[K]*call[K, V]
		)
		i := 0
		for _, rk := range rks {
			cl, shouldLoad := c.singleflight.startCall(rk.key, true)
			if shouldLoad {
				if rk.found {
					if toReloadCalls == nil {
						toReloadCalls = make(map[K]*call[K, V], len(rks)-i)
					}

					toReloadCalls[rk.key] = cl
				} else {
					if toLoadCalls == nil {
						toLoadCalls = make(map[K]*call[K, V], len(rks)-i)
					}

					toLoadCalls[rk.key] = cl
				}
			}
			i++
		}

		ctx := context.Background()
		if len(toLoadCalls) > 0 {
			func() {
				loadCtx, cancel := context.WithCancel(ctx)
				defer cancel()

				loadErr := c.wrapLoad(func() error {
					return c.singleflight.doBulkCall(loadCtx, toLoadCalls, bulkLoader.BulkLoad, c.afterDeleteCall)
				})
				if loadErr != nil {
					c.logger.Error(loadCtx, "BulkLoad returned an error", loadErr)
				}
			}()
		}
		if len(toReloadCalls) > 0 {
			func() {
				reloadCtx, cancel := context.WithCancel(ctx)
				defer cancel()

				reloadErr := c.wrapLoad(func() error {
					return c.singleflight.doBulkCall(reloadCtx, toReloadCalls, bulkLoader.BulkReload, c.afterDeleteCall)
				})
				if reloadErr != nil {
					c.logger.Error(reloadCtx, "BulkReload returned an error", reloadErr)
				}
			}()
		}
		// all calls?
	}()
}

// BulkGet returns the value associated with key in this cache, obtaining that value from loader if necessary.
// The method improves upon the conventional "if cached, return; otherwise create, cache and return" pattern.
//
// If another call to Get (BulkGet) is currently loading the value for key,
// simply waits for that goroutine to finish and returns its loaded value. Note that
// multiple goroutines can concurrently load values for distinct keys.
//
// No observable state associated with this cache is modified until loading completes.
//
// WARNING: BulkLoader.BulkLoad must not attempt to update any mappings of this cache directly.
//
// WARNING: For any given key, every bulkLoader used with it should compute the same value.
// Otherwise, a call that passes one bulkLoader may return the result of another call
// with a differently behaving bulkLoader. For example, a call that requests a short timeout
// for an RPC may wait for a similar call that requests a long timeout, or a call by an
// unprivileged user may return a resource accessible only to a privileged user making a similar call.
func (c *Cache[K, V]) BulkGet(ctx context.Context, keys []K, bulkLoader BulkLoader[K, V]) (map[K]V, error) {
	c.singleflight.init()

	offset := c.clock.Offset()
	result := make(map[K]V, len(keys))
	var (
		misses    map[K]*call[K, V]
		toRefresh []refreshableKey[K]
	)
	for _, key := range keys {
		if _, found := result[key]; found {
			continue
		}
		if _, found := misses[key]; found {
			continue
		}

		n := c.getNode(key, offset)
		if n != nil {
			if c.withRefresh && !n.IsFresh(offset) {
				if toRefresh == nil {
					toRefresh = make([]refreshableKey[K], 0, len(keys)-len(result))
				}

				toRefresh = append(toRefresh, refreshableKey[K]{
					key:   key,
					found: true,
				})
			}

			result[key] = n.Value()
			continue
		}

		if misses == nil {
			misses = make(map[K]*call[K, V], len(keys)-len(result))
		}
		misses[key] = nil
	}

	c.bulkRefreshKeys(toRefresh, bulkLoader)
	if len(misses) == 0 {
		return result, nil
	}

	loadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if c.withStats {
		c.clock.Init()
	}

	var toLoadCalls map[K]*call[K, V]
	i := 0
	for key := range misses {
		// node.Node compute?
		cl, shouldLoad := c.singleflight.startCall(key, false)
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
			return c.singleflight.doBulkCall(loadCtx, toLoadCalls, bulkLoader.BulkLoad, c.afterDeleteCall)
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
		if _, ok := toLoadCalls[key]; ok || cl.isNotFound {
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

// Refresh loads a new value for the key, asynchronously. While the new value is loading the
// previous value (if any) will continue to be returned by any Get unless it is evicted.
// If the new value is loaded successfully, it will replace the previous value in the cache;
// If refreshing returned an error, the previous value will remain,
// and the error will be logged using Logger (if it's not ErrNotFound) and swallowed. If another goroutine is currently
// loading the value for key, then this method does not perform an additional load.
//
// Cache will call Loader.Reload if the cache currently contains a value for the key,
// and Loader.Load otherwise.
//
// WARNING: Loader.Load and Loader.Reload must not attempt to update any mappings of this cache directly.
//
// WARNING: For any given key, every loader used with it should compute the same value.
// Otherwise, a call that passes one loader may return the result of another call
// with a differently behaving loader. For example, a call that requests a short timeout
// for an RPC may wait for a similar call that requests a long timeout, or a call by an
// unprivileged user may return a resource accessible only to a privileged user making a similar call.
func (c *Cache[K, V]) Refresh(key K, loader Loader[K, V]) {
	if !c.withRefresh {
		return
	}

	c.singleflight.init()

	offset := c.clock.Offset()
	n := c.getNode(key, offset)
	found := n != nil

	c.refreshKey(refreshableKey[K]{
		key:   key,
		found: found,
	}, loader)
}

// BulkRefresh loads a new value for each key, asynchronously. While the new value is loading the
// previous value (if any) will continue to be returned by any Get unless it is evicted.
// If the new value is loaded successfully, it will replace the previous value in the cache;
// If refreshing returned an error, the previous value will remain,
// and the error will be logged using Logger and swallowed. If another goroutine is currently
// loading the value for key, then this method does not perform an additional load.
//
// Cache will call BulkLoader.BulkReload for existing keys, and BulkLoader.BulkLoad otherwise.
//
// WARNING: BulkLoader.BulkLoad and BulkLoader.BulkReload must not attempt to update any mappings of this cache directly.
//
// WARNING: For any given key, every bulkLoader used with it should compute the same value.
// Otherwise, a call that passes one bulkLoader may return the result of another call
// with a differently behaving loader. For example, a call that requests a short timeout
// for an RPC may wait for a similar call that requests a long timeout, or a call by an
// unprivileged user may return a resource accessible only to a privileged user making a similar call.
func (c *Cache[K, V]) BulkRefresh(keys []K, bulkLoader BulkLoader[K, V]) {
	if !c.withRefresh || len(keys) == 0 {
		return
	}

	c.singleflight.init()

	uniq := make(map[K]struct{}, len(keys))
	for _, k := range keys {
		uniq[k] = struct{}{}
	}

	offset := c.clock.Offset()
	toRefresh := make([]refreshableKey[K], 0, len(uniq))
	for key := range uniq {
		n := c.getNode(key, offset)
		toRefresh = append(toRefresh, refreshableKey[K]{
			key:   key,
			found: n != nil,
		})
	}

	c.bulkRefreshKeys(toRefresh, bulkLoader)
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

func (c *Cache[K, V]) expire() {
	offset := c.clock.Refresh()
	c.evictionMutex.Lock()
	c.expirationPolicy.DeleteExpired(offset, c.evictOrExpireNode)
	c.evictionMutex.Unlock()
}

func (c *Cache[K, V]) periodicExpire() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.doneClose:
			return
		case <-ticker.C:
			c.expire()
		}
	}
}

func (c *Cache[K, V]) evictOrExpireNode(n node.Node[K, V], nowNanos int64) {
	deleted := c.deleteNodeFromMap(n)
	if deleted != nil {
		c.policy.Delete(n)
		c.expirationPolicy.Delete(n)

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

func (c *Cache[K, V]) onWrite(t task[K, V], offset int64, isBackground bool) {
	if t.isClose() {
		if isBackground {
			if c.withExpiration {
				c.doneClose <- struct{}{}
			}
			c.doneClose <- struct{}{}
		}
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
		c.onWrite(t, offset, true)
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

// All returns an iterator over all entries in the cache.
//
// Iterator is at least weakly consistent: he is safe for concurrent use,
// but if the cache is modified (including by eviction) after the iterator is
// created, it is undefined which of the changes (if any) will be reflected in that iterator.
func (c *Cache[K, V]) All() iter.Seq2[K, V] {
	offset := c.clock.Offset()
	return func(yield func(K, V) bool) {
		c.hashmap.Range(func(n node.Node[K, V]) bool {
			if !n.IsAlive() || n.HasExpired(offset) {
				return true
			}

			return yield(n.Key(), n.Value())
		})
	}
}

// InvalidateAll discards all entries in the cache. The behavior of this operation is undefined for an entry
// that is being loaded (or reloaded) and is otherwise not present.
func (c *Cache[K, V]) InvalidateAll() {
	c.evictionMutex.Lock()

	if c.withEviction {
		c.stripedBuffer.DrainTo(func(n node.Node[K, V]) {})
		c.policy.Clear()
	}
	if c.withExpiration {
		c.expirationPolicy.Clear()
	}
	c.cleanUpWriteBuffer()
	c.hashmap.Clear()

	c.evictionMutex.Unlock()
}

func (c *Cache[K, V]) cleanUpWriteBuffer() {
	hasClose := false
	if c.withProcess {
		offset := c.clock.Offset()
		for {
			t, ok := c.writeBuffer.TryPop()
			if !ok {
				break
			}
			c.onWrite(t, offset, false)
			if t.isClose() {
				hasClose = true
			}
		}
	}
	if hasClose {
		c.writeBuffer.Push(newCloseTask[K, V]())
	}
}

// CleanUp performs any pending maintenance operations needed by the cache. Exactly which activities are
// performed -- if any -- is implementation-dependent.
func (c *Cache[K, V]) CleanUp() {
	c.evictionMutex.Lock()
	defer c.evictionMutex.Unlock()

	if c.withEviction {
		c.stripedBuffer.DrainTo(c.policy.Read)
	}
	if c.withExpiration {
		c.expirationPolicy.DeleteExpired(c.clock.Offset(), c.evictOrExpireNode)
	}

	c.cleanUpWriteBuffer()
}

// Close discards all entries in the cache and stop all goroutines.
//
// NOTE: this operation must be performed when no requests are made to the cache otherwise the behavior is undefined.
func (c *Cache[K, V]) Close() {
	c.closeOnce.Do(func() {
		if c.withProcess {
			c.writeBuffer.Push(newCloseTask[K, V]())
		}
		c.InvalidateAll()
		if c.withProcess {
			<-c.doneClose
		}
	})
}

// EstimatedSize returns the approximate number of entries in this cache. The value returned is an estimate; the
// actual count may differ if there are concurrent insertions or deletions, or if some entries are
// pending deletion due to expiration. In the case of stale entries
// this inaccuracy can be mitigated by performing a CleanUp first.
func (c *Cache[K, V]) EstimatedSize() int {
	return c.hashmap.Size()
}
