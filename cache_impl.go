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
	"fmt"
	"iter"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maypok86/otter/v2/internal/clock"
	"github.com/maypok86/otter/v2/internal/deque/queue"
	"github.com/maypok86/otter/v2/internal/eviction/tinylfu"
	"github.com/maypok86/otter/v2/internal/expiration"
	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/hashmap"
	"github.com/maypok86/otter/v2/internal/lossy"
	"github.com/maypok86/otter/v2/internal/xmath"
	"github.com/maypok86/otter/v2/internal/xruntime"
	"github.com/maypok86/otter/v2/stats"
)

const (
	unreachableExpiresAfter     = xruntime.MaxDuration
	unreachableRefreshableAfter = xruntime.MaxDuration
	noTime                      = int64(0)

	minWriteBufferSize = 4
	writeBufferRetries = 100
)

const (
	// A drain is not taking place.
	idle uint32 = 0
	// A drain is required due to a pending write modification.
	required uint32 = 1
	// A drain is in progress and will transition to idle.
	processingToIdle uint32 = 2
	// A drain is in progress and will transition to required.
	processingToRequired uint32 = 3
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

func zeroValue[V any]() V {
	var v V
	return v
}

// cache is a structure performs a best-effort bounding of a hash table using eviction algorithm
// to determine which entries to evict when the capacity is exceeded.
type cache[K comparable, V any] struct {
	drainStatus        atomic.Uint32
	_                  [xruntime.CacheLineSize - 4]byte
	nodeManager        *node.Manager[K, V]
	hashmap            *hashmap.Map[K, V, node.Node[K, V]]
	evictionPolicy     *tinylfu.Policy[K, V]
	expirationPolicy   *expiration.Variable[K, V]
	stats              stats.Recorder
	logger             Logger
	clock              *clock.Real
	readBuffer         *lossy.Striped[K, V]
	writeBuffer        *queue.MPSC[task[K, V]]
	executor           func(fn func())
	singleflight       *group[K, V]
	evictionMutex      sync.Mutex
	doneClose          chan struct{}
	weigher            func(key K, value V) uint32
	onDeletion         func(e DeletionEvent[K, V])
	onAtomicDeletion   func(e DeletionEvent[K, V])
	expiryCalculator   ExpiryCalculator[K, V]
	refreshCalculator  RefreshCalculator[K, V]
	taskPool           sync.Pool
	hasDefaultExecutor bool
	withTime           bool
	withExpiration     bool
	withRefresh        bool
	withEviction       bool
	isWeighted         bool
	withMaintenance    bool
	withStats          bool
}

// newCache returns a new cache instance based on the settings from Options.
func newCache[K comparable, V any](o *Options[K, V]) *cache[K, V] {
	withWeight := o.MaximumWeight > 0
	nodeManager := node.NewManager[K, V](node.Config{
		WithSize:       o.MaximumSize > 0,
		WithExpiration: o.ExpiryCalculator != nil,
		WithRefresh:    o.RefreshCalculator != nil,
		WithWeight:     withWeight,
	})

	maximum := o.getMaximum()
	withEviction := maximum > 0

	var readBuffer *lossy.Striped[K, V]
	if withEviction {
		readBuffer = lossy.NewStriped(maxStripedBufferSize, nodeManager)
	}

	withStats := o.StatsRecorder != nil
	if !withStats {
		o.StatsRecorder = &stats.NoopRecorder{}
	}

	c := &cache[K, V]{
		nodeManager:        nodeManager,
		hashmap:            hashmap.NewWithSize[K, V, node.Node[K, V]](nodeManager, o.getInitialCapacity()),
		stats:              o.StatsRecorder,
		logger:             o.getLogger(),
		readBuffer:         readBuffer,
		singleflight:       &group[K, V]{},
		executor:           o.getExecutor(),
		hasDefaultExecutor: o.Executor == nil,
		weigher:            o.getWeigher(),
		onDeletion:         o.OnDeletion,
		onAtomicDeletion:   o.OnAtomicDeletion,
		clock:              &clock.Real{},
		withStats:          withStats,
		expiryCalculator:   o.ExpiryCalculator,
		refreshCalculator:  o.RefreshCalculator,
		isWeighted:         withWeight,
	}

	c.withEviction = withEviction
	if c.withEviction {
		c.evictionPolicy = tinylfu.NewPolicy[K, V](withWeight)
		if o.hasInitialCapacity() {
			//nolint:gosec // there's no overflow
			c.evictionPolicy.EnsureCapacity(min(maximum, uint64(o.getInitialCapacity())))
		}
	}

	if o.ExpiryCalculator != nil {
		c.expirationPolicy = expiration.NewVariable(nodeManager)
	}

	c.withExpiration = o.ExpiryCalculator != nil
	c.withRefresh = o.RefreshCalculator != nil
	c.withTime = c.withExpiration || c.withRefresh
	c.withMaintenance = c.withEviction || c.withExpiration

	if c.withMaintenance {
		c.writeBuffer = queue.NewMPSC[task[K, V]](minWriteBufferSize, maxWriteBufferSize)
	}
	if c.withTime {
		c.clock.Init()
	}
	if c.withExpiration {
		c.doneClose = make(chan struct{})
		go c.periodicCleanUp()
	}

	if c.withEviction {
		c.SetMaximum(maximum)
	}

	return c
}

func (c *cache[K, V]) newNode(key K, value V, old node.Node[K, V]) node.Node[K, V] {
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

func (c *cache[K, V]) nodeToEntry(n node.Node[K, V], offset int64) Entry[K, V] {
	nowNano := noTime
	if c.withTime {
		nowNano = offset
	}

	expiresAt := int64(unreachableExpiresAfter)
	if c.withExpiration {
		expiresAt = n.ExpiresAt()
	}

	refreshableAt := int64(unreachableRefreshableAfter)
	if c.withRefresh {
		refreshableAt = n.RefreshableAt()
	}

	return Entry[K, V]{
		Key:               n.Key(),
		Value:             n.Value(),
		Weight:            n.Weight(),
		ExpiresAtNano:     expiresAt,
		RefreshableAtNano: refreshableAt,
		SnapshotAtNano:    nowNano,
	}
}

// has checks if there is an item with the given key in the cache.
func (c *cache[K, V]) has(key K) bool {
	_, ok := c.GetIfPresent(key)
	return ok
}

// GetIfPresent returns the value associated with the key in this cache.
func (c *cache[K, V]) GetIfPresent(key K) (V, bool) {
	offset := c.clock.Offset()
	n := c.getNode(key, offset)
	if n == nil {
		return zeroValue[V](), false
	}

	return n.Value(), true
}

// getNode returns the node associated with the key in this cache.
func (c *cache[K, V]) getNode(key K, offset int64) node.Node[K, V] {
	n := c.hashmap.Get(key)
	if n == nil {
		c.stats.RecordMisses(1)
		if c.drainStatus.Load() == required {
			c.scheduleDrainBuffers()
		}
		return nil
	}
	if n.HasExpired(offset) {
		c.stats.RecordMisses(1)
		c.scheduleDrainBuffers()
		return nil
	}

	c.afterRead(n, offset, true, true)

	return n
}

// getNodeQuietly returns the node associated with the key in this cache.
//
// Unlike getNode, this function does not produce any side effects
// such as updating statistics or the eviction policy.
func (c *cache[K, V]) getNodeQuietly(key K, offset int64) node.Node[K, V] {
	n := c.hashmap.Get(key)
	if n == nil || !n.IsAlive() || n.HasExpired(offset) {
		return nil
	}

	return n
}

func (c *cache[K, V]) afterRead(got node.Node[K, V], offset int64, recordHit, calcExpiresAt bool) {
	if recordHit {
		c.stats.RecordHits(1)
	}

	if calcExpiresAt {
		c.calcExpiresAtAfterRead(got, offset)
	}

	delayable := c.skipReadBuffer() || c.readBuffer.Add(got) != lossy.Full
	if c.shouldDrainBuffers(delayable) {
		c.scheduleDrainBuffers()
	}
}

// Set associates the value with the key in this cache.
//
// If the specified key is not already associated with a value, then it returns new value and true.
//
// If the specified key is already associated with a value, then it returns existing value and false.
func (c *cache[K, V]) Set(key K, value V) (V, bool) {
	return c.set(key, value, false)
}

// SetIfAbsent if the specified key is not already associated with a value associates it with the given value.
//
// If the specified key is not already associated with a value, then it returns new value and true.
//
// If the specified key is already associated with a value, then it returns existing value and false.
func (c *cache[K, V]) SetIfAbsent(key K, value V) (V, bool) {
	return c.set(key, value, true)
}

func (c *cache[K, V]) calcExpiresAtAfterRead(n node.Node[K, V], offset int64) {
	if !c.withExpiration {
		return
	}

	expiresAfter := c.expiryCalculator.ExpireAfterRead(c.nodeToEntry(n, offset))
	c.setExpiresAfterRead(n, offset, expiresAfter)
}

func (c *cache[K, V]) setExpiresAfterRead(n node.Node[K, V], offset int64, expiresAfter time.Duration) {
	if expiresAfter <= 0 {
		return
	}

	expiresAt := n.ExpiresAt()
	currentDuration := time.Duration(expiresAt - offset)
	diff := xmath.Abs(int64(expiresAfter - currentDuration))
	if diff > 0 {
		n.CASExpiresAt(expiresAt, offset+int64(expiresAfter))
	}
}

// GetEntry returns the cache entry associated with the key in this cache.
func (c *cache[K, V]) GetEntry(key K) (Entry[K, V], bool) {
	offset := c.clock.Offset()
	n := c.getNode(key, offset)
	if n == nil {
		return Entry[K, V]{}, false
	}
	return c.nodeToEntry(n, offset), true
}

// GetEntryQuietly returns the cache entry associated with the key in this cache.
//
// Unlike GetEntry, this function does not produce any side effects
// such as updating statistics or the eviction policy.
func (c *cache[K, V]) GetEntryQuietly(key K) (Entry[K, V], bool) {
	offset := c.clock.Offset()
	n := c.getNodeQuietly(key, offset)
	if n == nil {
		return Entry[K, V]{}, false
	}
	return c.nodeToEntry(n, offset), true
}

// SetExpiresAfter specifies that the entry should be automatically removed from the cache once the duration has
// elapsed. The expiration policy determines when the entry's age is reset.
func (c *cache[K, V]) SetExpiresAfter(key K, expiresAfter time.Duration) {
	if !c.withExpiration || expiresAfter <= 0 {
		return
	}

	offset := c.clock.Offset()
	n := c.hashmap.Get(key)
	if n == nil {
		return
	}

	c.setExpiresAfterRead(n, offset, expiresAfter)
	c.afterRead(n, offset, false, false)
}

// SetRefreshableAfter specifies that each entry should be eligible for reloading once a fixed duration has elapsed.
// The refresh policy determines when the entry's age is reset.
func (c *cache[K, V]) SetRefreshableAfter(key K, refreshableAfter time.Duration) {
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

func (c *cache[K, V]) calcExpiresAtAfterWrite(n, old node.Node[K, V], offset int64) {
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

func (c *cache[K, V]) set(key K, value V, onlyIfAbsent bool) (V, bool) {
	var (
		old node.Node[K, V]
		n   node.Node[K, V]
	)
	offset := c.clock.Offset()
	c.hashmap.Compute(key, func(current node.Node[K, V]) node.Node[K, V] {
		old = current
		if onlyIfAbsent && current != nil {
			// no op
			c.calcExpiresAtAfterRead(old, offset)
			return current
		}
		// set
		c.singleflight.delete(key)
		n = c.newNode(key, value, old)
		c.calcExpiresAtAfterWrite(n, old, offset)
		c.calcRefreshableAt(n, old, nil, offset)
		c.makeRetired(old)
		if old != nil {
			cause := CauseReplacement
			if old.HasExpired(offset) {
				cause = CauseExpiration
			}
			c.notifyAtomicDeletion(old.Key(), old.Value(), cause)
		}
		return n
	})
	if onlyIfAbsent {
		if old == nil {
			c.afterWrite(n, nil, offset)
			return value, true
		}
		c.afterRead(old, offset, false, false)
		return old.Value(), false
	}

	c.afterWrite(n, old, offset)
	if old != nil {
		return old.Value(), false
	}
	return value, true
}

func (c *cache[K, V]) afterWrite(n, old node.Node[K, V], offset int64) {
	if !c.withMaintenance {
		if old != nil {
			c.notifyDeletion(old.Key(), old.Value(), CauseReplacement)
		}
		return
	}

	if old == nil {
		// insert
		c.afterWriteTask(c.getTask(n, nil, addReason, causeUnknown))
		return
	}

	// update
	cause := CauseReplacement
	if old.HasExpired(offset) {
		cause = CauseExpiration
	}

	c.afterWriteTask(c.getTask(n, old, updateReason, cause))
}

type refreshableKey[K comparable, V any] struct {
	key K
	old node.Node[K, V]
}

func (c *cache[K, V]) refreshKey(ctx context.Context, rk refreshableKey[K, V], loader Loader[K, V]) <-chan RefreshResult[K, V] {
	if !c.withRefresh {
		return nil
	}

	ch := make(chan RefreshResult[K, V], 1)

	c.executor(func() {
		var refresher func(ctx context.Context, key K) (V, error)
		if rk.old != nil {
			refresher = func(ctx context.Context, key K) (V, error) {
				return loader.Reload(ctx, key, rk.old.Value())
			}
		} else {
			refresher = loader.Load
		}

		loadCtx, cancel := context.WithCancel(ctx)
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

		ch <- RefreshResult[K, V]{
			Key:   cl.key,
			Value: cl.value,
			Err:   cl.err,
		}
	})

	return ch
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
func (c *cache[K, V]) Get(ctx context.Context, key K, loader Loader[K, V]) (V, error) {
	c.singleflight.init()

	offset := c.clock.Offset()
	n := c.getNode(key, offset)
	if n != nil {
		if !n.IsFresh(offset) {
			c.refreshKey(ctx, refreshableKey[K, V]{
				key: n.Key(),
				old: n,
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

func (c *cache[K, V]) calcRefreshableAt(n, old node.Node[K, V], cl *call[K, V], offset int64) {
	if !c.withRefresh {
		return
	}

	var refreshableAfter time.Duration
	entry := c.nodeToEntry(n, offset)
	currentDuration := entry.RefreshableAfter()
	//nolint:gocritic // it's ok
	if cl != nil && cl.isRefresh && old != nil {
		if cl.isNotFound {
			return
		}
		if cl.err != nil {
			refreshableAfter = c.refreshCalculator.RefreshAfterReloadFailure(entry, cl.err)
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

func (c *cache[K, V]) afterDeleteCall(cl *call[K, V]) {
	var (
		inserted bool
		deleted  bool
		old      node.Node[K, V]
	)
	offset := c.clock.Offset()
	newNode := c.hashmap.Compute(cl.key, func(oldNode node.Node[K, V]) node.Node[K, V] {
		defer cl.cancel()

		isCorrectCall := cl.isFake || c.singleflight.deleteCall(cl)
		old = oldNode
		if isCorrectCall && cl.isNotFound {
			if oldNode != nil {
				deleted = true
				c.makeRetired(oldNode)
				c.notifyAtomicDeletion(oldNode.Key(), oldNode.Value(), CauseInvalidation)
			}
			return nil
		}
		if cl.err != nil {
			if cl.isRefresh && oldNode != nil {
				c.calcRefreshableAt(oldNode, oldNode, cl, offset)
			}
			return oldNode
		}
		if !isCorrectCall {
			return oldNode
		}
		inserted = true
		n := c.newNode(cl.key, cl.value, old)
		c.calcExpiresAtAfterWrite(n, old, offset)
		c.calcRefreshableAt(n, old, cl, offset)
		c.makeRetired(old)
		if old != nil {
			cause := CauseReplacement
			if old.HasExpired(offset) {
				cause = CauseExpiration
			}
			c.notifyAtomicDeletion(old.Key(), old.Value(), cause)
		}
		return n
	})
	if deleted {
		c.afterDelete(old, offset, false)
	}
	if inserted {
		c.afterWrite(newNode, old, offset)
	}
}

func (c *cache[K, V]) bulkRefreshKeys(ctx context.Context, rks []refreshableKey[K, V], bulkLoader BulkLoader[K, V]) <-chan []RefreshResult[K, V] {
	if !c.withRefresh {
		return nil
	}
	ch := make(chan []RefreshResult[K, V], 1)
	if len(rks) == 0 {
		ch <- []RefreshResult[K, V]{}
		return ch
	}

	c.executor(func() {
		var (
			toLoadCalls   map[K]*call[K, V]
			toReloadCalls map[K]*call[K, V]
			foundCalls    []*call[K, V]
		)
		results := make([]RefreshResult[K, V], 0, len(rks))
		i := 0
		for _, rk := range rks {
			cl, shouldLoad := c.singleflight.startCall(rk.key, true)
			if shouldLoad {
				if rk.old != nil {
					if toReloadCalls == nil {
						toReloadCalls = make(map[K]*call[K, V], len(rks)-i)
					}
					cl.value = rk.old.Value()

					toReloadCalls[rk.key] = cl
				} else {
					if toLoadCalls == nil {
						toLoadCalls = make(map[K]*call[K, V], len(rks)-i)
					}

					toLoadCalls[rk.key] = cl
				}
			} else {
				if foundCalls == nil {
					foundCalls = make([]*call[K, V], 0, len(rks)-i)
				}
				foundCalls = append(foundCalls, cl)
			}
			i++
		}

		if len(toLoadCalls) > 0 {
			loadErr := c.wrapLoad(func() error {
				return c.singleflight.doBulkCall(ctx, toLoadCalls, bulkLoader.BulkLoad, c.afterDeleteCall)
			})
			if loadErr != nil {
				c.logger.Error(ctx, "BulkLoad returned an error", loadErr)
			}

			for _, cl := range toLoadCalls {
				results = append(results, RefreshResult[K, V]{
					Key:   cl.key,
					Value: cl.value,
					Err:   cl.err,
				})
			}
		}
		if len(toReloadCalls) > 0 {
			reload := func(ctx context.Context, keys []K) (map[K]V, error) {
				oldValues := make([]V, 0, len(keys))
				for _, k := range keys {
					cl := toReloadCalls[k]
					oldValues = append(oldValues, cl.value)
					cl.value = zeroValue[V]()
				}
				return bulkLoader.BulkReload(ctx, keys, oldValues)
			}

			reloadErr := c.wrapLoad(func() error {
				return c.singleflight.doBulkCall(ctx, toReloadCalls, reload, c.afterDeleteCall)
			})
			if reloadErr != nil {
				c.logger.Error(ctx, "BulkReload returned an error", reloadErr)
			}

			for _, cl := range toReloadCalls {
				results = append(results, RefreshResult[K, V]{
					Key:   cl.key,
					Value: cl.value,
					Err:   cl.err,
				})
			}
		}
		for _, cl := range foundCalls {
			cl.wait()
			results = append(results, RefreshResult[K, V]{
				Key:   cl.key,
				Value: cl.value,
				Err:   cl.err,
			})
		}
		ch <- results
	})

	return ch
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
func (c *cache[K, V]) BulkGet(ctx context.Context, keys []K, bulkLoader BulkLoader[K, V]) (map[K]V, error) {
	c.singleflight.init()

	offset := c.clock.Offset()
	result := make(map[K]V, len(keys))
	var (
		misses    map[K]*call[K, V]
		toRefresh []refreshableKey[K, V]
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
					toRefresh = make([]refreshableKey[K, V], 0, len(keys)-len(result))
				}

				toRefresh = append(toRefresh, refreshableKey[K, V]{
					key: key,
					old: n,
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

	c.bulkRefreshKeys(ctx, toRefresh, bulkLoader)
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
			result[key] = cl.Value()
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

func (c *cache[K, V]) wrapLoad(fn func() error) error {
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
// cache will call Loader.Reload if the cache currently contains a value for the key,
// and Loader.Load otherwise.
//
// WARNING: Loader.Load and Loader.Reload must not attempt to update any mappings of this cache directly.
//
// WARNING: For any given key, every loader used with it should compute the same value.
// Otherwise, a call that passes one loader may return the result of another call
// with a differently behaving loader. For example, a call that requests a short timeout
// for an RPC may wait for a similar call that requests a long timeout, or a call by an
// unprivileged user may return a resource accessible only to a privileged user making a similar call.
func (c *cache[K, V]) Refresh(ctx context.Context, key K, loader Loader[K, V]) <-chan RefreshResult[K, V] {
	if !c.withRefresh {
		return nil
	}

	c.singleflight.init()

	offset := c.clock.Offset()
	n := c.getNode(key, offset)

	return c.refreshKey(ctx, refreshableKey[K, V]{
		key: key,
		old: n,
	}, loader)
}

// BulkRefresh loads a new value for each key, asynchronously. While the new value is loading the
// previous value (if any) will continue to be returned by any Get unless it is evicted.
// If the new value is loaded successfully, it will replace the previous value in the cache;
// If refreshing returned an error, the previous value will remain,
// and the error will be logged using Logger and swallowed. If another goroutine is currently
// loading the value for key, then this method does not perform an additional load.
//
// cache will call BulkLoader.BulkReload for existing keys, and BulkLoader.BulkLoad otherwise.
//
// WARNING: BulkLoader.BulkLoad and BulkLoader.BulkReload must not attempt to update any mappings of this cache directly.
//
// WARNING: For any given key, every bulkLoader used with it should compute the same value.
// Otherwise, a call that passes one bulkLoader may return the result of another call
// with a differently behaving loader. For example, a call that requests a short timeout
// for an RPC may wait for a similar call that requests a long timeout, or a call by an
// unprivileged user may return a resource accessible only to a privileged user making a similar call.
func (c *cache[K, V]) BulkRefresh(ctx context.Context, keys []K, bulkLoader BulkLoader[K, V]) <-chan []RefreshResult[K, V] {
	if !c.withRefresh {
		return nil
	}

	c.singleflight.init()

	uniq := make(map[K]struct{}, len(keys))
	for _, k := range keys {
		uniq[k] = struct{}{}
	}

	offset := c.clock.Offset()
	toRefresh := make([]refreshableKey[K, V], 0, len(uniq))
	for key := range uniq {
		n := c.getNode(key, offset)
		toRefresh = append(toRefresh, refreshableKey[K, V]{
			key: key,
			old: n,
		})
	}

	return c.bulkRefreshKeys(ctx, toRefresh, bulkLoader)
}

// Invalidate discards any cached value for the key.
//
// Returns previous value if any. The invalidated result reports whether the key was
// present.
func (c *cache[K, V]) Invalidate(key K) (value V, invalidated bool) {
	var d node.Node[K, V]
	offset := c.clock.Offset()
	c.hashmap.Compute(key, func(n node.Node[K, V]) node.Node[K, V] {
		c.singleflight.delete(key)
		if n != nil {
			d = n
			c.makeRetired(d)
			c.notifyAtomicDeletion(d.Key(), d.Value(), CauseInvalidation)
		}
		return nil
	})
	c.afterDelete(d, offset, false)
	if d != nil {
		return d.Value(), true
	}
	return zeroValue[V](), false
}

func (c *cache[K, V]) deleteNodeFromMap(n node.Node[K, V], cause DeletionCause) node.Node[K, V] {
	var deleted node.Node[K, V]
	c.hashmap.Compute(n.Key(), func(current node.Node[K, V]) node.Node[K, V] {
		c.singleflight.delete(n.Key())
		if current == nil {
			return nil
		}
		if n.AsPointer() == current.AsPointer() {
			deleted = current
			c.makeRetired(deleted)
			c.notifyAtomicDeletion(deleted.Key(), deleted.Value(), cause)
			return nil
		}
		return current
	})
	return deleted
}

func (c *cache[K, V]) deleteNode(n node.Node[K, V], offset int64) {
	c.afterDelete(c.deleteNodeFromMap(n, CauseInvalidation), offset, true)
}

func (c *cache[K, V]) afterDelete(deleted node.Node[K, V], offset int64, withLock bool) {
	if deleted == nil {
		return
	}

	if !c.withMaintenance {
		c.notifyDeletion(deleted.Key(), deleted.Value(), CauseInvalidation)
		return
	}

	// delete
	cause := CauseInvalidation
	if deleted.HasExpired(offset) {
		cause = CauseExpiration
	}

	t := c.getTask(deleted, nil, deleteReason, cause)
	if withLock {
		c.runTask(t)
	} else {
		c.afterWriteTask(c.getTask(deleted, nil, deleteReason, cause))
	}
}

func (c *cache[K, V]) notifyDeletion(key K, value V, cause DeletionCause) {
	if c.onDeletion == nil {
		return
	}

	c.executor(func() {
		c.onDeletion(DeletionEvent[K, V]{
			Key:   key,
			Value: value,
			Cause: cause,
		})
	})
}

func (c *cache[K, V]) notifyAtomicDeletion(key K, value V, cause DeletionCause) {
	if c.onAtomicDeletion == nil {
		return
	}

	c.onAtomicDeletion(DeletionEvent[K, V]{
		Key:   key,
		Value: value,
		Cause: cause,
	})
}

func (c *cache[K, V]) periodicCleanUp() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.doneClose:
			return
		case <-ticker.C:
			c.CleanUp()
		}
	}
}

func (c *cache[K, V]) evictNode(n node.Node[K, V], nowNanos int64) {
	cause := CauseOverflow
	if n.HasExpired(nowNanos) {
		cause = CauseExpiration
	}

	deleted := c.deleteNodeFromMap(n, cause) != nil

	if c.withEviction {
		c.evictionPolicy.Delete(n)
	}
	if c.withExpiration {
		c.expirationPolicy.Delete(n)
	}

	c.makeDead(n)

	if deleted {
		c.notifyDeletion(n.Key(), n.Value(), cause)
		c.stats.RecordEviction(n.Weight())
	}
}

// All returns an iterator over all entries in the cache.
//
// Iterator is at least weakly consistent: he is safe for concurrent use,
// but if the cache is modified (including by eviction) after the iterator is
// created, it is undefined which of the changes (if any) will be reflected in that iterator.
func (c *cache[K, V]) All() iter.Seq2[K, V] {
	offset := c.clock.Offset()
	return func(yield func(K, V) bool) {
		c.hashmap.Range(func(n node.Node[K, V]) bool {
			if !n.IsAlive() || n.HasExpired(offset) {
				c.scheduleDrainBuffers()
				return true
			}

			return yield(n.Key(), n.Value())
		})
	}
}

// InvalidateAll discards all entries in the cache. The behavior of this operation is undefined for an entry
// that is being loaded (or reloaded) and is otherwise not present.
func (c *cache[K, V]) InvalidateAll() {
	c.evictionMutex.Lock()

	if !c.skipReadBuffer() {
		c.readBuffer.DrainTo(func(n node.Node[K, V]) {})
	}
	if c.withMaintenance {
		for {
			t := c.writeBuffer.TryPop()
			if t == nil {
				break
			}
			c.runTask(t)
		}
	}
	// Discard all entries, falling back to one-by-one to avoid excessive lock hold times
	nodes := make([]node.Node[K, V], 0, c.EstimatedSize())
	threshold := uint64(maxWriteBufferSize / 2)
	c.hashmap.Range(func(n node.Node[K, V]) bool {
		nodes = append(nodes, n)
		return true
	})
	offset := c.clock.Offset()
	for len(nodes) > 0 && c.writeBuffer.Size() < threshold {
		n := nodes[len(nodes)-1]
		nodes = nodes[:len(nodes)-1]
		c.deleteNode(n, offset)
	}

	c.evictionMutex.Unlock()

	for _, n := range nodes {
		c.Invalidate(n.Key())
	}
}

// CleanUp performs any pending maintenance operations needed by the cache. Exactly which activities are
// performed -- if any -- is implementation-dependent.
func (c *cache[K, V]) CleanUp() {
	c.performCleanUp(nil)
}

func (c *cache[K, V]) shouldDrainBuffers(delayable bool) bool {
	drainStatus := c.drainStatus.Load()
	switch drainStatus {
	case idle:
		return !delayable
	case required:
		return true
	case processingToIdle, processingToRequired:
		return false
	default:
		panic(fmt.Sprintf("Invalid drain status: %d", drainStatus))
	}
}

func (c *cache[K, V]) skipReadBuffer() bool {
	return !c.withEviction
}

func (c *cache[K, V]) afterWriteTask(t *task[K, V]) {
	for i := 0; i < writeBufferRetries; i++ {
		if c.writeBuffer.TryPush(t) {
			c.scheduleAfterWrite()
			return
		}
		c.scheduleDrainBuffers()
		runtime.Gosched()
	}

	// In scenarios where the writing goroutines cannot make progress then they attempt to provide
	// assistance by performing the eviction work directly. This can resolve cases where the
	// maintenance task is scheduled but not running.
	c.evictionMutex.Lock()
	c.maintenance(t)
	c.evictionMutex.Unlock()
	c.rescheduleCleanUpIfIncomplete()
}

func (c *cache[K, V]) scheduleAfterWrite() {
	for {
		drainStatus := c.drainStatus.Load()
		switch drainStatus {
		case idle:
			c.drainStatus.CompareAndSwap(idle, required)
			c.scheduleDrainBuffers()
			return
		case required:
			c.scheduleDrainBuffers()
			return
		case processingToIdle:
			if c.drainStatus.CompareAndSwap(processingToIdle, processingToRequired) {
				return
			}
		case processingToRequired:
			return
		default:
			panic(fmt.Sprintf("Invalid drain status: %d", drainStatus))
		}
	}
}

func (c *cache[K, V]) scheduleDrainBuffers() {
	if c.drainStatus.Load() >= processingToIdle {
		return
	}

	if c.evictionMutex.TryLock() {
		drainStatus := c.drainStatus.Load()
		if drainStatus >= processingToIdle {
			c.evictionMutex.Unlock()
			return
		}

		c.drainStatus.Store(processingToIdle)

		var token atomic.Uint32
		c.executor(func() {
			c.drainBuffers(&token)
		})

		if token.CompareAndSwap(0, 1) {
			c.evictionMutex.Unlock()
		}
	}
}

func (c *cache[K, V]) drainBuffers(token *atomic.Uint32) {
	if c.evictionMutex.TryLock() {
		c.maintenance(nil)
		c.evictionMutex.Unlock()
		c.rescheduleCleanUpIfIncomplete()
	} else {
		// already locked
		if token.CompareAndSwap(0, 1) {
			// executor is sync
			c.maintenance(nil)
			c.evictionMutex.Unlock()
			c.rescheduleCleanUpIfIncomplete()
		} else {
			// executor is async
			c.performCleanUp(nil)
		}
	}
}

func (c *cache[K, V]) performCleanUp(t *task[K, V]) {
	c.evictionMutex.Lock()
	c.maintenance(t)
	c.evictionMutex.Unlock()
	c.rescheduleCleanUpIfIncomplete()
}

func (c *cache[K, V]) rescheduleCleanUpIfIncomplete() {
	if c.drainStatus.Load() != required {
		return
	}

	// An immediate scheduling cannot be performed on a custom executor because it may use a
	// caller-runs policy. This could cause the caller's penalty to exceed the amortized threshold,
	// e.g. repeated concurrent writes could result in a retry loop.
	if c.hasDefaultExecutor {
		c.scheduleDrainBuffers()
		return
	}
}

func (c *cache[K, V]) maintenance(t *task[K, V]) {
	c.drainStatus.Store(processingToIdle)

	c.drainReadBuffer()
	c.drainWriteBuffer()
	c.runTask(t)
	c.expireNodes()
	c.evictNodes()
	c.climb()

	if c.drainStatus.Load() != processingToIdle || !c.drainStatus.CompareAndSwap(processingToIdle, idle) {
		c.drainStatus.Store(required)
	}
}

func (c *cache[K, V]) drainReadBuffer() {
	if c.skipReadBuffer() {
		return
	}

	c.readBuffer.DrainTo(c.onAccess)
}

func (c *cache[K, V]) drainWriteBuffer() {
	for i := uint32(0); i <= maxWriteBufferSize; i++ {
		t := c.writeBuffer.TryPop()
		if t == nil {
			return
		}
		c.runTask(t)
	}
	c.drainStatus.Store(processingToRequired)
}

func (c *cache[K, V]) runTask(t *task[K, V]) {
	if t == nil {
		return
	}

	n := t.node()
	switch t.writeReason {
	case addReason:
		if c.withExpiration && n.IsAlive() {
			c.expirationPolicy.Add(n)
		}
		if c.withEviction {
			c.evictionPolicy.Add(n, c.evictNode)
		}
	case updateReason:
		old := t.oldNode()
		if c.withExpiration {
			c.expirationPolicy.Delete(old)
			if n.IsAlive() {
				c.expirationPolicy.Add(n)
			}
		}
		if c.withEviction {
			c.evictionPolicy.Update(n, old, c.evictNode)
		}
		c.notifyDeletion(old.Key(), old.Value(), t.deletionCause)
	case deleteReason:
		if c.withExpiration {
			c.expirationPolicy.Delete(n)
		}
		if c.withEviction {
			c.evictionPolicy.Delete(n)
		}
		c.notifyDeletion(n.Key(), n.Value(), t.deletionCause)
	default:
		panic(fmt.Sprintf("Invalid task type: %d", t.writeReason))
	}

	c.putTask(t)
}

func (c *cache[K, V]) onAccess(n node.Node[K, V]) {
	if c.withEviction {
		c.evictionPolicy.Access(n)
	}
	if c.withExpiration && !node.Equals(n.NextExp(), nil) {
		c.expirationPolicy.Delete(n)
		if n.IsAlive() {
			c.expirationPolicy.Add(n)
		}
	}
}

func (c *cache[K, V]) expireNodes() {
	if c.withExpiration {
		c.expirationPolicy.DeleteExpired(c.clock.Offset(), c.evictNode)
	}
}

func (c *cache[K, V]) evictNodes() {
	if !c.withEviction {
		return
	}
	c.evictionPolicy.EvictNodes(c.evictNode)
}

func (c *cache[K, V]) climb() {
	if !c.withEviction {
		return
	}
	c.evictionPolicy.Climb()
}

func (c *cache[K, V]) getTask(n, old node.Node[K, V], writeReason reason, cause DeletionCause) *task[K, V] {
	t, ok := c.taskPool.Get().(*task[K, V])
	if !ok {
		return &task[K, V]{
			n:             n,
			old:           old,
			writeReason:   writeReason,
			deletionCause: cause,
		}
	}
	t.n = n
	t.old = old
	t.writeReason = writeReason
	t.deletionCause = cause

	return t
}

func (c *cache[K, V]) putTask(t *task[K, V]) {
	t.n = nil
	t.old = nil
	t.writeReason = unknownReason
	t.deletionCause = causeUnknown
	c.taskPool.Put(t)
}

// SetMaximum specifies the maximum total size of this cache. This value may be interpreted as the weighted
// or unweighted threshold size based on how this cache was constructed. If the cache currently
// exceeds the new maximum size this operation eagerly evict entries until the cache shrinks to
// the appropriate size.
func (c *cache[K, V]) SetMaximum(maximum uint64) {
	if !c.withEviction {
		return
	}
	c.evictionMutex.Lock()
	c.evictionPolicy.SetMaximumSize(maximum)
	c.maintenance(nil)
	c.evictionMutex.Unlock()
	c.rescheduleCleanUpIfIncomplete()
}

// GetMaximum returns the maximum total weighted or unweighted size of this cache, depending on how the
// cache was constructed.
func (c *cache[K, V]) GetMaximum() uint64 {
	c.evictionMutex.Lock()
	if c.drainStatus.Load() == required {
		c.maintenance(nil)
	}
	result := c.evictionPolicy.Maximum
	c.evictionMutex.Unlock()
	c.rescheduleCleanUpIfIncomplete()
	return result
}

// close discards all entries in the cache and stop all goroutines.
//
// NOTE: this operation must be performed when no requests are made to the cache otherwise the behavior is undefined.
func (c *cache[K, V]) close() {
	if c.withExpiration {
		c.doneClose <- struct{}{}
	}
}

// EstimatedSize returns the approximate number of entries in this cache. The value returned is an estimate; the
// actual count may differ if there are concurrent insertions or deletions, or if some entries are
// pending deletion due to expiration. In the case of stale entries
// this inaccuracy can be mitigated by performing a CleanUp first.
func (c *cache[K, V]) EstimatedSize() int {
	return c.hashmap.Size()
}

// WeightedSize returns the approximate accumulated weight of entries in this cache. If this cache does not
// use a weighted size bound, then the method will return 0.
func (c *cache[K, V]) WeightedSize() uint64 {
	if !c.isWeighted {
		return 0
	}

	c.evictionMutex.Lock()
	if c.drainStatus.Load() == required {
		c.maintenance(nil)
	}
	result := c.evictionPolicy.WeightedSize
	c.evictionMutex.Unlock()
	c.rescheduleCleanUpIfIncomplete()
	return result
}

func (c *cache[K, V]) makeRetired(n node.Node[K, V]) {
	if n != nil && c.withMaintenance && n.IsAlive() {
		n.Retire()
	}
}

func (c *cache[K, V]) makeDead(n node.Node[K, V]) {
	if !c.withMaintenance {
		return
	}

	if c.withEviction {
		c.evictionPolicy.MakeDead(n)
	} else if !n.IsDead() {
		n.Die()
	}
}
