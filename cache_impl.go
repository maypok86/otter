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
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maypok86/otter/v2/internal/deque/queue"
	"github.com/maypok86/otter/v2/internal/expiration"
	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/hashmap"
	"github.com/maypok86/otter/v2/internal/lossy"
	"github.com/maypok86/otter/v2/internal/xmath"
	"github.com/maypok86/otter/v2/internal/xruntime"
	"github.com/maypok86/otter/v2/stats"
)

const (
	unreachableExpiresAt     = int64(xruntime.MaxDuration)
	unreachableRefreshableAt = int64(xruntime.MaxDuration)
	noTime                   = int64(0)

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
	evictionPolicy     *policy[K, V]
	timerWheel         *expiration.Wheel[K, V]
	stats              stats.Recorder
	statsSnapshoter    stats.Snapshoter
	logger             Logger
	clock              timeSource
	statsClock         *realSource
	readBuffer         *lossy.Striped[K, V]
	writeBuffer        *queue.MPSC[task[K, V]]
	executor           func(fn func())
	singleflight       *group[K, V]
	evictionMutex      sync.Mutex
	doneStop           chan struct{}
	stopOnce           sync.Once
	weigher            func(key K, value V) uint32
	onDeletion         func(e DeletionEvent[K, V])
	onAtomicDeletion   func(e DeletionEvent[K, V])
	expiryCalculator   ExpiryCalculator[K, V]
	refreshCalculator  RefreshCalculator[K, V]
	hasDefaultExecutor bool
	withTime           bool
	withExpiration     bool
	withRefresh        bool
	withEviction       bool
	isWeighted         bool
	withMaintenance    bool
	withStats          bool
	// whether a read may change the expiration time: ExpireAfterRead is not called for the
	// built-in calculators whose reads always keep it
	withReadExpiry bool
	// the time of the current maintenance pass, read once when it starts (with expiration only),
	// so that the timer wheel and size eviction agree on what has expired; guarded by
	// evictionMutex
	maintenanceNanos int64
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

	withStats := o.StatsRecorder != nil
	if withStats {
		_, ok := o.StatsRecorder.(*stats.NoopRecorder)
		withStats = !ok
	}
	var logger Logger = &safeLogger{logger: o.getLogger()}
	var statsRecorder stats.Recorder = &stats.NoopRecorder{}
	var statsSnapshoter stats.Snapshoter = &stats.NoopRecorder{}
	if withStats {
		statsRecorder = &safeRecorder{recorder: o.StatsRecorder, logger: logger}
		if snapshoter, ok := o.StatsRecorder.(stats.Snapshoter); ok {
			statsSnapshoter = snapshoter
		}
	}

	c := &cache[K, V]{
		nodeManager:        nodeManager,
		hashmap:            hashmap.NewWithSize[K, V, node.Node[K, V]](nodeManager, o.getInitialCapacity()),
		stats:              statsRecorder,
		statsSnapshoter:    statsSnapshoter,
		logger:             logger,
		singleflight:       &group[K, V]{},
		executor:           o.getExecutor(),
		hasDefaultExecutor: o.Executor == nil,
		weigher:            o.getWeigher(),
		onDeletion:         o.OnDeletion,
		onAtomicDeletion:   o.OnAtomicDeletion,
		clock:              newTimeSource(o.Clock),
		statsClock:         &realSource{},
		expiryCalculator:   o.ExpiryCalculator,
		refreshCalculator:  o.RefreshCalculator,
		isWeighted:         withWeight,
		withStats:          withStats,
	}
	c.singleflight.commit = c.commitCall
	c.singleflight.publish = c.publish
	// read on every refresh, so that tests can replace the executor
	c.singleflight.executor = func(fn func()) { c.executor(fn) }

	if withStats {
		c.statsClock.Init()
	}

	c.withEviction = withEviction
	if c.withEviction {
		var expectedSize uint64
		if o.hasInitialCapacity() {
			//nolint:gosec // there's no overflow
			expectedSize = min(maximum, uint64(o.getInitialCapacity()))
		}
		c.evictionPolicy = newPolicy[K, V](withWeight, expectedSize, c.evictNode)
	}

	if o.ExpiryCalculator != nil {
		c.timerWheel = expiration.NewWheel(nodeManager)
	}

	c.withExpiration = o.ExpiryCalculator != nil
	_, creating := o.ExpiryCalculator.(*varExpiryCreating[K, V])
	_, writing := o.ExpiryCalculator.(*varExpiryWriting[K, V])
	c.withReadExpiry = c.withExpiration && !creating && !writing
	c.withRefresh = o.RefreshCalculator != nil
	c.withTime = c.withExpiration || c.withRefresh
	c.withMaintenance = c.withEviction || c.withExpiration

	if c.withMaintenance {
		c.readBuffer = lossy.NewStriped(maxStripedBufferSize, nodeManager)
		c.writeBuffer = queue.NewMPSC[task[K, V]](minWriteBufferSize, maxWriteBufferSize)
	}
	if c.withTime {
		c.clock.Init()
	}
	if c.withExpiration {
		c.doneStop = make(chan struct{})
		go c.periodicCleanUp()
	}

	if c.withEviction {
		c.SetMaximum(maximum)
	}

	return c
}

// newNode creates a node for a write whose weight and timestamps have already been computed.
// With updatable, the node can have its value replaced in place: a value that would be kept
// inline is kept behind an atomic pointer instead.
func (c *cache[K, V]) newNode(key K, value V, weight uint32, expiresAt, refreshableAt int64, updatable bool) node.Node[K, V] {
	if updatable {
		return c.nodeManager.CreateUpdatable(key, value, expiresAt, refreshableAt, weight)
	}
	return c.nodeManager.Create(key, value, expiresAt, refreshableAt, weight)
}

// writeEntry describes a write before it is applied: the new value and weight with the
// timestamps of the node it replaces, which are kept unless the calculators change them. old is
// nil for a creation, which sees no timestamps.
func (c *cache[K, V]) writeEntry(key K, value V, weight uint32, old node.Node[K, V], nanos int64) Entry[K, V] {
	nowNano := noTime
	if c.withTime {
		nowNano = nanos
	}

	expiresAt := unreachableExpiresAt
	if c.withExpiration && old != nil {
		expiresAt = old.ExpiresAt()
	}

	refreshableAt := unreachableRefreshableAt
	if c.withRefresh && old != nil {
		refreshableAt = old.RefreshableAt()
	}

	return Entry[K, V]{
		Key:               key,
		Value:             value,
		Weight:            weight,
		ExpiresAtNano:     expiresAt,
		RefreshableAtNano: refreshableAt,
		SnapshotAtNano:    nowNano,
	}
}

func (c *cache[K, V]) nodeToEntry(n node.Node[K, V], nanos int64) Entry[K, V] {
	nowNano := noTime
	if c.withTime {
		nowNano = nanos
	}

	expiresAt := unreachableExpiresAt
	if c.withExpiration {
		expiresAt = n.ExpiresAt()
	}

	refreshableAt := unreachableRefreshableAt
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
	nowNano := c.clock.NowNano()
	n := c.getNode(key, nowNano)
	if n == nil {
		return zeroValue[V](), false
	}

	return n.Value(), true
}

// getNode returns the node associated with the key in this cache.
func (c *cache[K, V]) getNode(key K, nowNano int64) node.Node[K, V] {
	n := c.hashmap.Get(key)
	if n == nil {
		c.stats.RecordMisses(1)
		if c.drainStatus.Load() == required {
			c.scheduleDrainBuffers()
		}
		return nil
	}
	if n.HasExpired(nowNano) {
		c.stats.RecordMisses(1)
		c.scheduleDrainBuffers()
		return nil
	}

	c.afterRead(n, nowNano, true, true)

	return n
}

// getNodeQuietly returns the node associated with the key in this cache.
//
// Unlike getNode, this function does not produce any side effects
// such as updating statistics or the eviction policy.
func (c *cache[K, V]) getNodeQuietly(key K, nowNano int64) node.Node[K, V] {
	n := c.hashmap.Get(key)
	if n == nil || !n.IsAlive() || n.HasExpired(nowNano) {
		return nil
	}

	return n
}

func (c *cache[K, V]) afterRead(got node.Node[K, V], nowNano int64, recordHit, calcExpiresAt bool) {
	if recordHit {
		c.stats.RecordHits(1)
	}

	if calcExpiresAt {
		c.calcExpiresAtAfterRead(got, nowNano)
	}

	delayable := c.skipReadBuffer() || c.readBuffer.Add(got) != lossy.Full
	if c.shouldDrainBuffers(delayable) {
		c.scheduleDrainBuffers()
	}
}

// Set associates the value with the key in this cache.
//
// If the specified key is not already associated with a value, then it returns new value and true.
// An expired entry that has not been removed yet counts as no value.
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

func (c *cache[K, V]) calcExpiresAtAfterRead(n node.Node[K, V], nowNano int64) {
	if !c.withReadExpiry {
		return
	}

	entry := c.nodeToEntry(n, nowNano)
	// The new deadline is derived from the entry the calculator was shown, so it may only replace
	// that entry's deadline. A writer can store a new deadline in place in the meantime, and
	// comparing against a fresh read of the node would revert it to one derived from the old value.
	if next, ok := expiresAtAfterRead(c.expiryCalculator, entry, nowNano); ok {
		n.CASExpiresAt(entry.ExpiresAtNano, next)
	}
}

// GetEntry returns the cache entry associated with the key in this cache.
func (c *cache[K, V]) GetEntry(key K) (Entry[K, V], bool) {
	nowNano := c.clock.NowNano()
	n := c.getNode(key, nowNano)
	if n == nil {
		return Entry[K, V]{}, false
	}
	return c.nodeToEntry(n, nowNano), true
}

// GetEntryQuietly returns the cache entry associated with the key in this cache.
//
// Unlike GetEntry, this function does not produce any side effects
// such as updating statistics or the eviction policy.
func (c *cache[K, V]) GetEntryQuietly(key K) (Entry[K, V], bool) {
	nowNano := c.clock.NowNano()
	n := c.getNodeQuietly(key, nowNano)
	if n == nil {
		return Entry[K, V]{}, false
	}
	return c.nodeToEntry(n, nowNano), true
}

// SetExpiresAfter specifies that the entry should be automatically removed from the cache once the duration has
// elapsed. The expiration policy determines when the entry's age is reset. It has no effect on an absent
// or expired entry.
func (c *cache[K, V]) SetExpiresAfter(key K, expiresAfter time.Duration) {
	if !c.withExpiration || expiresAfter <= 0 {
		return
	}

	nowNano := c.clock.NowNano()
	n := c.hashmap.Get(key)
	if n == nil {
		return
	}
	// An expired entry that is still in the table is absent: a new deadline would bring its
	// stale value back. The deadline is changed only from the one checked here, so a concurrent
	// writer's deadline is not overwritten either.
	expiresAt := n.ExpiresAt()
	if !n.IsAlive() || expiresAt <= nowNano {
		return
	}

	// The caller asked for this deadline, so it is set exactly.
	next, ok := moveDeadline(expiresAt, nowNano, expiresAfter, 0)
	earlier := ok && n.CASExpiresAt(expiresAt, next) && next < expiresAt
	c.afterRead(n, nowNano, false, false)
	if earlier {
		// The read buffer may drop the access, and the node would then wait in the timer wheel's
		// bucket for its former deadline, so an earlier deadline goes through the write buffer,
		// as for an in-place write. A later one needs nothing: the wheel re-checks the node when
		// its former bucket expires.
		c.afterWriteTask(newTask(n, nil, reconcileReason, causeUnknown))
	}
}

// SetRefreshableAfter specifies that each entry should be eligible for reloading once a fixed duration has elapsed.
// The refresh policy determines when the entry's age is reset.
func (c *cache[K, V]) SetRefreshableAfter(key K, refreshableAfter time.Duration) {
	if !c.withRefresh || refreshableAfter <= 0 {
		return
	}

	nowNano := c.clock.NowNano()
	n := c.hashmap.Get(key)
	if n == nil {
		return
	}

	refreshableAt := n.RefreshableAt()
	if next := nextDeadline(refreshableAt, nowNano, refreshableAfter); next != refreshableAt {
		n.SetRefreshableAt(next)
	}
}

func (c *cache[K, V]) set(key K, value V, onlyIfAbsent bool) (V, bool) {
	nowNano := c.clock.NowNano()
	m := c.mutate(key, nowNano, fromCaller, func(prev previous[K, V]) (mutationOp, V) {
		if onlyIfAbsent && prev.live {
			c.calcExpiresAtAfterRead(prev.node, nowNano)
			return opKeep, value
		}
		return opWrite, value
	})
	c.publish(m)
	// An expired entry that is still in the table is absent, as for reads.
	if !m.live {
		return value, true
	}
	if onlyIfAbsent {
		c.afterRead(m.node, nowNano, false, false)
	}
	return m.value, false
}

// Compute either sets the computed new value for the key,
// invalidates the value for the key, or does nothing, based on
// the returned [ComputeOp]. When the op returned by remappingFunc
// is [WriteOp], the value is updated to the new value. If
// it is [InvalidateOp], the entry is removed from the cache
// altogether. And finally, if the op is [CancelOp] then the
// entry is left as-is. In other words, if it did not already
// exist, it is not created, and if it did exist, it is not
// updated. This is useful to synchronously execute some
// operation on the value without incurring the cost of
// updating the cache every time.
//
// The ok result indicates whether the entry is present in the cache after the compute operation.
// The actualValue result contains the value of the cache
// if a corresponding entry is present, or the zero value otherwise.
// You can think of these results as equivalent to regular key-value lookups in a map.
//
// This call locks a hash table bucket while the compute function
// is executed. It means that modifications on other entries in
// the bucket will be blocked until the remappingFunc executes. Consider
// this when the function includes long-running operations.
func (c *cache[K, V]) Compute(
	key K,
	remappingFunc func(oldValue V, found bool) (newValue V, op ComputeOp),
) (V, bool) {
	return c.doCompute(key, remappingFunc, c.clock.NowNano(), true)
}

// ComputeIfAbsent returns the existing value for the key if
// present. Otherwise, it tries to compute the value using the
// provided function. If mappingFunc returns true as the cancel value, the computation is cancelled and the zero value
// for type V is returned.
//
// The ok result indicates whether the entry is present in the cache after the compute operation.
// The actualValue result contains the value of the cache
// if a corresponding entry is present, or the zero value
// otherwise. You can think of these results as equivalent to regular key-value lookups in a map.
//
// This call locks a hash table bucket while the compute function
// is executed. It means that modifications on other entries in
// the bucket will be blocked until the valueFn executes. Consider
// this when the function includes long-running operations.
func (c *cache[K, V]) ComputeIfAbsent(
	key K,
	mappingFunc func() (newValue V, cancel bool),
) (V, bool) {
	nowNano := c.clock.NowNano()
	if n := c.getNode(key, nowNano); n != nil {
		return n.Value(), true
	}

	return c.doCompute(key, func(oldValue V, found bool) (newValue V, op ComputeOp) {
		if found {
			return oldValue, CancelOp
		}
		newValue, cancel := mappingFunc()
		if cancel {
			return zeroValue[V](), CancelOp
		}
		return newValue, WriteOp
	}, nowNano, false)
}

// ComputeIfPresent returns the zero value for type V if the key is not found.
// Otherwise, it tries to compute the value using the provided function.
//
// ComputeIfPresent either sets the computed new value for the key,
// invalidates the value for the key, or does nothing, based on
// the returned [ComputeOp]. When the op returned by remappingFunc
// is [WriteOp], the value is updated to the new value. If
// it is [InvalidateOp], the entry is removed from the cache
// altogether. And finally, if the op is [CancelOp] then the
// entry is left as-is. In other words, if it did not already
// exist, it is not created, and if it did exist, it is not
// updated. This is useful to synchronously execute some
// operation on the value without incurring the cost of
// updating the cache every time.
//
// The ok result indicates whether the entry is present in the cache after the compute operation.
// The actualValue result contains the value of the cache
// if a corresponding entry is present, or the zero value
// otherwise. You can think of these results as equivalent to regular key-value lookups in a map.
//
// This call locks a hash table bucket while the compute function
// is executed. It means that modifications on other entries in
// the bucket will be blocked until the valueFn executes. Consider
// this when the function includes long-running operations.
func (c *cache[K, V]) ComputeIfPresent(
	key K,
	remappingFunc func(oldValue V) (newValue V, op ComputeOp),
) (V, bool) {
	nowNano := c.clock.NowNano()
	if n := c.getNode(key, nowNano); n == nil {
		return zeroValue[V](), false
	}

	return c.doCompute(key, func(oldValue V, found bool) (newValue V, op ComputeOp) {
		if found {
			return remappingFunc(oldValue)
		}
		return zeroValue[V](), CancelOp
	}, nowNano, false)
}

func (c *cache[K, V]) doCompute(
	key K,
	remappingFunc func(oldValue V, found bool) (newValue V, op ComputeOp),
	nowNano int64,
	recordStats bool,
) (V, bool) {
	var (
		result     V
		op         ComputeOp
		notValidOp bool
		panicErr   error
	)
	m := c.mutate(key, nowNano, fromCaller, func(prev previous[K, V]) (mutationOp, V) {
		var (
			oldValue    V
			actualValue V
		)
		if prev.live {
			oldValue = prev.value
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					panicErr = newPanicError(r)
				}
			}()

			actualValue, op = remappingFunc(oldValue, prev.live)
		}()
		if panicErr != nil {
			return opKeep, actualValue
		}
		switch op {
		case CancelOp:
			if prev.node != nil && !prev.live {
				// The expired entry is removed, as the timer wheel would remove it.
				return opRemoveExpired, actualValue
			}
			result = prev.value
			return opKeep, actualValue
		case WriteOp:
			result = actualValue
			return opWrite, actualValue
		case InvalidateOp:
			return opInvalidate, actualValue
		default:
			notValidOp = true
			return opKeep, actualValue
		}
	})
	if panicErr != nil {
		panic(panicErr)
	}
	if notValidOp {
		panic(fmt.Sprintf("otter: invalid ComputeOp: %d", op))
	}
	if recordStats {
		if m.live {
			c.stats.RecordHits(1)
		} else {
			c.stats.RecordMisses(1)
		}
	}
	c.publish(m)
	if m.next == nil {
		return zeroValue[V](), false
	}
	// The node may be updated in place by another writer once the bucket lock is released, so
	// the result is the value seen under the lock, not m.next.Value().
	return result, true
}

type refreshableKey[K comparable, V any] struct {
	key K
	old node.Node[K, V]
}

func (c *cache[K, V]) refreshKey(
	ctx context.Context,
	rk refreshableKey[K, V],
	loader Loader[K, V],
	isManual bool,
) <-chan RefreshResult[K, V] {
	if !c.withRefresh {
		return nil
	}

	var ch chan RefreshResult[K, V]
	if isManual {
		ch = make(chan RefreshResult[K, V], 1)
	}

	// The refresh is registered before it is handed to the executor, so that a write that
	// follows the request cancels it. Registered by the task, a refresh that the executor starts
	// after the write would replace the written value or bring an invalidated key back.
	// The clock is user code, so it is read before: if it panicked after the registration, the
	// call would never be finished, and every later refresh of the key would join it.
	nowNano := c.clock.NowNano()
	cl, shouldLoad := c.singleflight.startRefresh(rk.key, rk.old)
	if shouldLoad && !isManual {
		if n := c.refreshedMeanwhile(rk.key, nowNano); n != nil {
			c.singleflight.adopt(cl, n.Value())
			return ch
		}
	}
	var owned []*call[K, V]
	if shouldLoad {
		owned = []*call[K, V]{cl}
	}

	c.singleflight.submitRefresh(owned, func() {
		var refresher func(ctx context.Context, key K) (V, error)
		if rk.old != nil {
			refresher = func(ctx context.Context, key K) (V, error) {
				return loader.Reload(ctx, key, rk.old.Value())
			}
		} else {
			refresher = loader.Load
		}

		if shouldLoad {
			//nolint:errcheck // there is no need to check error
			_ = c.wrapRefresh(func() error {
				loadCtx := context.WithoutCancel(ctx)
				return c.singleflight.doCall(loadCtx, cl, refresher)
			})
		}
		cl.wait()

		if cl.err != nil && !cl.isNotFound {
			c.logger.Error(ctx, "Returned an error during the refreshing", cl.err)
		}

		if isManual {
			ch <- cl.refreshResult()
		}
	})

	return ch
}

// Get returns the value associated with key in this cache, obtaining that value from loader if necessary.
// The method improves upon the conventional "if cached, return; otherwise create, cache and return" pattern.
//
// Get can return an ErrNotFound error if the Loader returns it, or if Get waits for a BulkGet
// whose BulkLoader leaves the key out of its result.
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

	nowNano := c.clock.NowNano()
	n := c.getNode(key, nowNano)
	if n != nil {
		// Read before the refresh starts: the node may be updated in place by then (with a
		// synchronous executor, by the refresh itself).
		value := n.Value()
		if !n.IsFresh(nowNano) {
			c.refreshKey(ctx, refreshableKey[K, V]{
				key: n.Key(),
				old: n,
			}, loader, false)
		}
		return value, nil
	}

	for {
		value, retry, err := c.loadOrJoin(ctx, key, loader, nowNano)
		if !retry {
			return value, err
		}
		nowNano = c.clock.NowNano()
	}
}

// loadOrJoin loads the value for key, which Get has not found, or waits for the load already
// running for it. retry reports that the joined load failed for its owner's context while ctx
// is live, so the key must be loaded again.
func (c *cache[K, V]) loadOrJoin(
	ctx context.Context,
	key K,
	loader Loader[K, V],
	nowNano int64,
) (value V, retry bool, err error) {
	cl, shouldLoad := c.singleflight.startLoad(key)
	if shouldLoad {
		// Re-check the map before loading. The getNode above runs before this
		// call slot is claimed, so it can miss a value that a concurrently
		// completing load publishes in between, leaving this caller to load the
		// same value a second time. group.finish frees a call slot only after
		// committing the value to the table, so winning the slot means any such
		// value is already visible here; adopt it rather than loading it again. Quiet lookup: the
		// getNode above already recorded the miss for this request.
		if n := c.getNodeQuietly(key, nowNano); n != nil {
			value := n.Value()
			c.singleflight.adopt(cl, value)
			return value, false, nil
		}
		//nolint:errcheck // there is no need to check error
		_ = c.wrapLoad(func() error {
			return c.singleflight.doCall(ctx, cl, loader.Load)
		})
		return cl.value, false, cl.err
	}

	if err := cl.waitContext(ctx); err != nil {
		return zeroValue[V](), false, err
	}
	if cl.failedForOwner() && ctx.Err() == nil {
		return zeroValue[V](), true, nil
	}
	return cl.value, false, cl.err
}

// commitCall writes the result of a finished load or refresh, if it may still be written, for
// group.finish: not found removes the entry, an error keeps it (a failed refresh moves its
// refresh time), and a value is written over it.
func (c *cache[K, V]) commitCall(cl *call[K, V]) mutation[K, V] {
	from := fromLoad
	if cl.isRefresh {
		from = fromRefresh
	}
	nowNano := c.clock.NowNano()
	return c.mutate(cl.key, nowNano, from, func(prev previous[K, V]) (mutationOp, V) {
		// From here on, an eviction or expiration of the key drops the call (see
		// group.deleteOnRemoval): it can no longer bring the removed value back.
		cl.committed = true
		if !cl.isRefresh && prev.live {
			// A load starts on a miss, so a write has given the key a value during the load. The
			// write may not have cancelled the load: a write that inserts or replaces a node
			// cancels the calls before the node is stored, so a load registered in between is not
			// cancelled. The value stays, and the load returns it, whatever it loaded, as a Get that
			// ran after the write would.
			cl.value, cl.err, cl.isNotFound = prev.value, nil, false
			return opKeep, cl.value
		}
		authoritative := c.singleflight.authoritative(cl, prev, nowNano)
		if authoritative && cl.isNotFound {
			return opInvalidate, cl.value
		}
		if cl.err != nil {
			// A failed refresh of an absent key leaves an expired node that it matched as it is.
			if authoritative && cl.isRefresh && prev.live && c.withRefresh {
				entry := c.nodeToEntry(prev.node, nowNano)
				prev.node.SetRefreshableAt(refreshableAtAfterWrite(c.refreshCalculator, reloadFailure, entry, entry.Value, cl.err, nowNano))
			}
			return opKeep, cl.value
		}
		if !authoritative {
			return opKeep, cl.value
		}
		return opWrite, cl.value
	})
}

// refreshedMeanwhile returns the node of key if it is live and fresh, so that a refresh that a
// read requested when it saw the key stale is no longer needed: the node has been refreshed or
// replaced since. A refresh frees its call slot only after it has published its value, so a
// refresh that has won the slot sees one that completed meanwhile. Without this check, every read
// that saw the key stale before the first refresh completed would reload it again.
//
// The skipped refresh is finished with the value of the returned node, since a Get of the key
// may join its call: that is the value the Get would read. A key that has been removed or has
// expired is refreshed as before, as there is no value to give.
func (c *cache[K, V]) refreshedMeanwhile(key K, nowNano int64) node.Node[K, V] {
	if n := c.getNodeQuietly(key, nowNano); n != nil && n.IsFresh(nowNano) {
		return n
	}
	return nil
}

func (c *cache[K, V]) bulkRefreshKeys(
	ctx context.Context,
	rks []refreshableKey[K, V],
	bulkLoader BulkLoader[K, V],
	isManual bool,
) <-chan []RefreshResult[K, V] {
	if !c.withRefresh {
		return nil
	}
	var ch chan []RefreshResult[K, V]
	if isManual {
		ch = make(chan []RefreshResult[K, V], 1)
	}
	if len(rks) == 0 {
		if isManual {
			ch <- []RefreshResult[K, V]{}
		}
		return ch
	}

	// Registered before the task is submitted, with the clock read before, as in refreshKey.
	nowNano := c.clock.NowNano()
	var (
		toLoadCalls   map[K]*call[K, V]
		toReloadCalls map[K]*call[K, V]
		foundCalls    []*call[K, V]
		owned         []*call[K, V]
	)
	for i, rk := range rks {
		cl, shouldLoad := c.singleflight.startRefresh(rk.key, rk.old)
		if shouldLoad && !isManual {
			if n := c.refreshedMeanwhile(rk.key, nowNano); n != nil {
				c.singleflight.adopt(cl, n.Value())
				continue
			}
		}
		if shouldLoad {
			owned = append(owned, cl)
			if rk.old != nil {
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
		} else {
			if foundCalls == nil {
				foundCalls = make([]*call[K, V], 0, len(rks)-i)
			}
			foundCalls = append(foundCalls, cl)
		}
	}

	c.singleflight.submitRefresh(owned, func() {
		var results []RefreshResult[K, V]
		if isManual {
			results = make([]RefreshResult[K, V], 0, len(rks))
		}

		loadCtx := context.WithoutCancel(ctx)
		if len(toLoadCalls) > 0 {
			loadErr := c.wrapRefresh(func() error {
				return c.singleflight.doBulkCall(loadCtx, toLoadCalls, bulkLoader.BulkLoad)
			})
			if loadErr != nil {
				c.logger.Error(ctx, "BulkLoad returned an error", loadErr)
			}

			if isManual {
				for _, cl := range toLoadCalls {
					// the bulk loader may have added keys that were not requested
					if cl.isFake {
						continue
					}
					results = append(results, cl.refreshResult())
				}
			}
		}
		if len(toReloadCalls) > 0 {
			reload := func(ctx context.Context, keys []K) (map[K]V, error) {
				oldValues := make([]V, 0, len(keys))
				for _, k := range keys {
					// read when the reload starts, as for a single refresh: a write that changed
					// it since the request has cancelled the refresh
					oldValues = append(oldValues, toReloadCalls[k].base.Value())
				}
				return bulkLoader.BulkReload(ctx, keys, oldValues)
			}

			reloadErr := c.wrapRefresh(func() error {
				return c.singleflight.doBulkCall(loadCtx, toReloadCalls, reload)
			})
			if reloadErr != nil {
				c.logger.Error(ctx, "BulkReload returned an error", reloadErr)
			}

			if isManual {
				for _, cl := range toReloadCalls {
					if cl.isFake {
						continue
					}
					results = append(results, cl.refreshResult())
				}
			}
		}
		for _, cl := range foundCalls {
			cl.wait()
			if isManual {
				results = append(results, cl.refreshResult())
			}
		}
		if isManual {
			ch <- results
		}
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

	nowNano := c.clock.NowNano()
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

		n := c.getNode(key, nowNano)
		if n != nil {
			if !n.IsFresh(nowNano) {
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

	c.bulkRefreshKeys(ctx, toRefresh, bulkLoader, false)

	var errsFromCalls []error
	for len(misses) > 0 {
		var err error
		misses, errsFromCalls, err = c.bulkLoadOrJoin(ctx, misses, bulkLoader, result, errsFromCalls)
		if err != nil {
			return result, err
		}
	}

	var err error
	if len(errsFromCalls) > 0 {
		err = errors.Join(errsFromCalls...)
	}

	return result, err
}

// bulkLoadOrJoin loads the values for misses, the keys BulkGet has not found, and waits for the
// loads already running for some of them. It adds the loaded values to result and the errors of
// joined loads to errsFromCalls. It returns the keys whose joined load failed for its owner's
// context while ctx is live, which must be loaded again, and an error that ends BulkGet: the bulk
// load failed or ctx is done.
func (c *cache[K, V]) bulkLoadOrJoin(
	ctx context.Context,
	misses map[K]*call[K, V],
	bulkLoader BulkLoader[K, V],
	result map[K]V,
	errsFromCalls []error,
) (map[K]*call[K, V], []error, error) {
	nowNano := c.clock.NowNano()
	var toLoadCalls map[K]*call[K, V]
	i := 0
	for key := range misses {
		cl, shouldLoad := c.singleflight.startLoad(key)
		if shouldLoad {
			// Winning the call slot means that a value written before is visible, see Get.
			if n := c.getNodeQuietly(key, nowNano); n != nil {
				value := n.Value()
				c.singleflight.adopt(cl, value)
				result[key] = value
				delete(misses, key)
				i++
				continue
			}
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
			return c.singleflight.doBulkCall(ctx, toLoadCalls, bulkLoader.BulkLoad)
		})
	}
	if loadErr != nil {
		return nil, errsFromCalls, loadErr
	}

	var retry map[K]*call[K, V]
	i = 0
	for key, cl := range misses {
		if err := cl.waitContext(ctx); err != nil {
			return nil, errsFromCalls, err
		}
		i++

		if cl.err == nil {
			result[key] = cl.Value()
			continue
		}
		if _, ok := toLoadCalls[key]; ok || cl.isNotFound {
			continue
		}
		if cl.failedForOwner() && ctx.Err() == nil {
			if retry == nil {
				retry = make(map[K]*call[K, V], len(misses)-i+1)
			}
			retry[key] = nil
			continue
		}
		if errsFromCalls == nil {
			errsFromCalls = make([]error, 0, len(misses)-i+1)
		}
		errsFromCalls = append(errsFromCalls, cl.err)
	}

	return retry, errsFromCalls, nil
}

func (c *cache[K, V]) wrapLoad(fn func() error) (err error) {
	startTime := c.statsClock.NowNano()

	// fn panics if writing a loaded value panics. The load is still recorded, as a failure,
	// since the caller gets the panic instead of the value.
	completed := false
	defer func() {
		loadTime := time.Duration(c.statsClock.NowNano() - startTime)
		if completed && (err == nil || errors.Is(err, ErrNotFound)) {
			c.stats.RecordLoadSuccess(loadTime)
		} else {
			c.stats.RecordLoadFailure(loadTime)
		}
	}()

	err = fn()
	completed = true

	var pe *panicError
	if errors.As(err, &pe) {
		panic(pe)
	}

	return err
}

// wrapRefresh is wrapLoad for refreshes, which run on the executor: a panic of the loader, or of
// writing its result, is returned as an error instead of crashing the executor's goroutine. The
// refreshed calls already hold that error, so it reaches the refresh results and the log like
// any other refresh error.
func (c *cache[K, V]) wrapRefresh(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if pe, ok := r.(*panicError); ok {
				err = pe
				return
			}
			err = newPanicError(r)
		}
	}()
	return c.wrapLoad(fn)
}

// Refresh loads a new value for the key, asynchronously. While the new value is loading the
// previous value (if any) will continue to be returned by any Get unless it is evicted.
// If the new value is loaded successfully, it will replace the previous value in the cache;
// If refreshing returned an error, the previous value will remain,
// and the error will be logged using Logger (if it's not ErrNotFound) and swallowed. If another goroutine is currently
// loading the value for key, then this method does not perform an additional load.
// If the key is written, invalidated or expires after the refresh was requested, the loaded
// value is discarded and the cache keeps the newer state.
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

	nowNano := c.clock.NowNano()
	n := c.getNodeQuietly(key, nowNano)

	return c.refreshKey(ctx, refreshableKey[K, V]{
		key: key,
		old: n,
	}, loader, true)
}

// BulkRefresh loads a new value for each key, asynchronously. While the new value is loading the
// previous value (if any) will continue to be returned by any Get unless it is evicted.
// If the new value is loaded successfully, it will replace the previous value in the cache;
// If refreshing returned an error, the previous value will remain,
// and the error will be logged using Logger and swallowed. If another goroutine is currently
// loading the value for key, then this method does not perform an additional load.
// If the key is written, invalidated or expires after the refresh was requested, the loaded
// value is discarded and the cache keeps the newer state.
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

	nowNano := c.clock.NowNano()
	toRefresh := make([]refreshableKey[K, V], 0, len(uniq))
	for key := range uniq {
		n := c.getNodeQuietly(key, nowNano)
		toRefresh = append(toRefresh, refreshableKey[K, V]{
			key: key,
			old: n,
		})
	}

	return c.bulkRefreshKeys(ctx, toRefresh, bulkLoader, true)
}

// Invalidate discards any cached value for the key.
//
// Returns previous value if any. The invalidated result reports whether the key was
// present; an expired entry that has not been removed yet is not.
func (c *cache[K, V]) Invalidate(key K) (value V, invalidated bool) {
	nowNano := c.clock.NowNano()
	m := c.mutate(key, nowNano, fromCaller, func(prev previous[K, V]) (mutationOp, V) {
		return opInvalidate, prev.value
	})
	c.publish(m)
	// An expired entry that was still in the table is removed, but it was not present.
	if m.live {
		return m.value, true
	}
	return zeroValue[V](), false
}

// nodeRemoval is why maintenance removes a given node, rather than whatever its key holds.
type nodeRemoval uint8

const (
	// removeForSize: the eviction policy chose the node. The node is kept if a writer has set its
	// weight to zero in place since then: a zero-weight entry is never evicted for size.
	removeForSize nodeRemoval = iota
	// removeExpired: the timer wheel found the node expired. The node is kept if a writer has
	// updated it in place since then, extending its lifetime.
	removeExpired
	// removeInvalidated: InvalidateAll discards the node, and cancels the key's pending loads.
	removeInvalidated
)

// cause is the deletion cause of a removal, unless the node turns out to have expired.
func (r nodeRemoval) cause() DeletionCause {
	switch r {
	case removeForSize:
		return CauseOverflow
	case removeExpired:
		return CauseExpiration
	default:
		return CauseInvalidation
	}
}

// deleteNodeFromMap removes n from the hash table if it is still there, and returns the cause
// reported to the synchronous listener. kept reports that n stays, as described by removal.
func (c *cache[K, V]) deleteNodeFromMap(
	n node.Node[K, V],
	nowNano int64,
	removal nodeRemoval,
) (deleted node.Node[K, V], deletedCause DeletionCause, kept bool) {
	c.hashmap.Compute(n.Key(), func(current node.Node[K, V]) node.Node[K, V] {
		// n is not nil, so a current n is in the table
		isCurrent := n == current
		if isCurrent {
			switch {
			case removal == removeExpired && !current.HasExpired(nowNano):
				// updated in place, extending its lifetime, after it was found expired
				kept = true
				return current
			case removal == removeForSize && c.isWeighted && current.Weight() == 0 &&
				!current.HasExpired(nowNano):
				// The policy chose the node by the weight it accounted, but a writer has since set
				// its weight to zero in place. The node stays linked until its reweigh task brings
				// the accounted weight to zero.
				kept = true
				return current
			}
		}
		if removal == removeInvalidated {
			c.singleflight.delete(n.Key())
		} else if isCurrent {
			// An eviction must not cancel an in-flight load: it was most likely started
			// because this very node has expired, and dropping it would make the loaded
			// value silently disappear. Only a pending refresh of the evicted value, or a load
			// that has already committed it, is dropped, and only if n is still the key's node:
			// the policy may evict a node that was already replaced, and the call then belongs
			// to the value that replaced it.
			c.singleflight.deleteOnRemoval(n.Key())
		}
		if !isCurrent {
			return current
		}
		deleted = current
		deletedCause = getCause(deleted, nowNano, removal.cause())
		c.makeRetired(deleted)
		c.notifyAtomicDeletion(deleted.Key(), deleted.Value(), deletedCause)
		return nil
	})
	return deleted, deletedCause, kept
}

func (c *cache[K, V]) deleteNode(n node.Node[K, V], nowNano int64) bool {
	deleted, cause, _ := c.deleteNodeFromMap(n, nowNano, removeInvalidated)
	c.afterDelete(deleted, cause, true, false)
	return deleted != nil
}

// afterDelete takes the cause that the synchronous listener was given under the lock.
func (c *cache[K, V]) afterDelete(deleted node.Node[K, V], cause DeletionCause, alreadyLocked, onExecutor bool) {
	if deleted == nil {
		return
	}

	if !c.withMaintenance {
		c.notifyDeletionFrom(onExecutor, deleted.Key(), deleted.Value(), cause)
		return
	}

	// delete
	t := newTask(deleted, nil, deleteReason, cause)
	if alreadyLocked {
		c.runTask(t)
	} else {
		c.afterWriteTask(t)
	}
}

func (c *cache[K, V]) notifyDeletion(key K, value V, cause DeletionCause) {
	c.notifyDeletionFrom(false, key, value, cause)
}

// notifyDeletionFrom calls the OnDeletion listener directly if the caller already runs as an
// executor task, and submits it to the executor otherwise. Submitting another task from a task
// deadlocks an executor that runs a bounded number of tasks and blocks the submitter: once all
// of its slots are taken by such callers, none of them can finish.
func (c *cache[K, V]) notifyDeletionFrom(onExecutor bool, key K, value V, cause DeletionCause) {
	if c.onDeletion == nil {
		return
	}

	if onExecutor {
		c.callOnDeletion(key, value, cause)
		return
	}

	// A panic of the listener, or of a custom executor submitting it, is logged: this may run
	// during maintenance, under the eviction lock.
	defer recoverCallback(c.logger, "Executor panicked while submitting OnDeletion")
	c.executor(func() {
		c.callOnDeletion(key, value, cause)
	})
}

func (c *cache[K, V]) callOnDeletion(key K, value V, cause DeletionCause) {
	defer recoverCallback(c.logger, "OnDeletion panicked")
	c.onDeletion(DeletionEvent[K, V]{
		Key:   key,
		Value: value,
		Cause: cause,
	})
}

func (c *cache[K, V]) notifyAtomicDeletion(key K, value V, cause DeletionCause) {
	if c.onAtomicDeletion == nil {
		return
	}

	// The listener runs under the hash table's bucket lock, after the entry is already changed
	// or removed; a panic is logged so that the change completes.
	defer recoverCallback(c.logger, "OnAtomicDeletion panicked")
	c.onAtomicDeletion(DeletionEvent[K, V]{
		Key:   key,
		Value: value,
		Cause: cause,
	})
}

func (c *cache[K, V]) periodicCleanUp() {
	tick := c.clock.Tick(time.Second)
	for {
		select {
		case <-c.doneStop:
			return
		case <-tick:
			c.CleanUp()
			c.clock.ProcessTick()
		}
	}
}

// evictNode removes n, which the eviction policy chose to evict for size, and reports whether n
// leaves the policy, which then unlinks it. The policy has no clock, so the time of the
// maintenance pass is used: an entry that has expired, but that the timer wheel has not reached
// yet, is reported as expired, as every other path reports it.
func (c *cache[K, V]) evictNode(n node.Node[K, V]) bool {
	return c.removeNode(n, c.maintenanceNanos, removeForSize)
}

// expireNode is called by the timer wheel for a node whose expiration time has passed, and
// reports whether it removed the node. The wheel reads that time before the hash table's lock is
// taken, and a writer can update the node in place in between, extending its lifetime. Such a
// node stays in the cache, and the wheel schedules it again.
func (c *cache[K, V]) expireNode(n node.Node[K, V], nowNanos int64) bool {
	if !c.removeNode(n, nowNanos, removeExpired) {
		return false
	}
	c.makeDead(n)
	return true
}

// removeNode removes n from the hash table and the timer wheel, and reports the removal unless n
// had already left the table (it was replaced or removed, and that is reported by its own task).
// It returns false if n is kept, as described by removal. The caller unlinks n from the eviction
// policy.
func (c *cache[K, V]) removeNode(n node.Node[K, V], nowNanos int64, removal nodeRemoval) bool {
	deleted, cause, kept := c.deleteNodeFromMap(n, nowNanos, removal)
	if kept {
		return false
	}
	if c.withExpiration {
		c.timerWheel.Unschedule(n)
	}
	if deleted != nil {
		// counted first, so that the listener sees the eviction it is told about
		c.stats.RecordEviction(n.Weight())
		c.notifyDeletion(n.Key(), n.Value(), cause)
	}
	return true
}

func (c *cache[K, V]) nodes() iter.Seq[node.Node[K, V]] {
	return func(yield func(node.Node[K, V]) bool) {
		c.hashmap.Range(func(n node.Node[K, V]) bool {
			nowNano := c.clock.NowNano()
			if !n.IsAlive() || n.HasExpired(nowNano) {
				c.scheduleDrainBuffers()
				return true
			}

			return yield(n)
		})
	}
}

func (c *cache[K, V]) entries() iter.Seq[Entry[K, V]] {
	return func(yield func(Entry[K, V]) bool) {
		for n := range c.nodes() {
			if !yield(c.nodeToEntry(n, c.clock.NowNano())) {
				return
			}
		}
	}
}

// All returns an iterator over all entries in the cache.
//
// Iterator is at least weakly consistent: he is safe for concurrent use,
// but if the cache is modified (including by eviction) after the iterator is
// created, it is undefined which of the changes (if any) will be reflected in that iterator.
func (c *cache[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for n := range c.nodes() {
			if !yield(n.Key(), n.Value()) {
				return
			}
		}
	}
}

// Keys returns an iterator over all keys in the cache.
// The iteration order is not specified and is not guaranteed to be the same from one call to the next.
//
// Iterator is at least weakly consistent: he is safe for concurrent use,
// but if the cache is modified (including by eviction) after the iterator is
// created, it is undefined which of the changes (if any) will be reflected in that iterator.
func (c *cache[K, V]) Keys() iter.Seq[K] {
	return func(yield func(K) bool) {
		for n := range c.nodes() {
			if !yield(n.Key()) {
				return
			}
		}
	}
}

// Values returns an iterator over all values in the cache.
// The iteration order is not specified and is not guaranteed to be the same from one call to the next.
//
// Iterator is at least weakly consistent: he is safe for concurrent use,
// but if the cache is modified (including by eviction) after the iterator is
// created, it is undefined which of the changes (if any) will be reflected in that iterator.
func (c *cache[K, V]) Values() iter.Seq[V] {
	return func(yield func(V) bool) {
		for n := range c.nodes() {
			if !yield(n.Value()) {
				return
			}
		}
	}
}

// InvalidateAll discards all entries in the cache. The behavior of this operation is undefined for an entry
// that is being loaded (or reloaded) and is otherwise not present.
func (c *cache[K, V]) InvalidateAll() {
	nodes := c.invalidateAllLocked()

	for _, n := range nodes {
		c.Invalidate(n.Key())
	}
}

// invalidateAllLocked discards entries under evictionMutex while the write buffer has room, and
// returns the rest to be invalidated one by one, together with the keys whose node was replaced
// after the snapshot.
func (c *cache[K, V]) invalidateAllLocked() []node.Node[K, V] {
	c.evictionMutex.Lock()
	defer c.evictionMutex.Unlock()

	if c.withMaintenance {
		// the replayed writes can evict
		c.readMaintenanceTime()
		c.readBuffer.DrainTo(func(n node.Node[K, V]) {})
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
	nowNano := c.clock.NowNano()
	var replaced []node.Node[K, V]
	for len(nodes) > 0 && c.writeBuffer.Size() < threshold {
		n := nodes[len(nodes)-1]
		nodes = nodes[:len(nodes)-1]
		if !c.deleteNode(n, nowNano) && c.hashmap.Get(n.Key()) != nil {
			// The key's node was replaced after the snapshot, e.g. by a reload that completed in
			// the meantime. The new node's insertion may still be in the write buffer, so it is
			// not removed here under the lock: the key is invalidated with the rest.
			replaced = append(replaced, n)
		}
	}
	return append(nodes, replaced...)
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
	return !c.withMaintenance || // without read buffer
		(!c.withExpiration && c.withEviction && c.evictionPolicy.sketch.isNotInitialized())
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
	c.performCleanUp(t)
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
		func() {
			// A panic of a custom executor, or of maintenance run by a synchronous one, is logged:
			// the caller's write is already done. If the drain did not take over the lock, it is
			// released here, and the drain is left required for the next attempt.
			defer func() {
				if r := recover(); r != nil {
					if token.CompareAndSwap(0, 1) {
						c.drainStatus.Store(required)
						c.evictionMutex.Unlock()
					}
					c.logger.Error(context.Background(), "Maintenance panicked", newPanicError(r))
				}
			}()
			c.executor(func() {
				c.drainBuffers(&token)
			})
		}()

		if token.CompareAndSwap(0, 1) {
			c.evictionMutex.Unlock()
		}
	}
}

func (c *cache[K, V]) drainBuffers(token *atomic.Uint32) {
	if c.evictionMutex.TryLock() {
		c.maintenanceAndUnlock(nil)
		c.rescheduleCleanUpIfIncomplete()
	} else {
		// already locked
		if token.CompareAndSwap(0, 1) {
			// executor is sync
			c.maintenanceAndUnlock(nil)
			c.rescheduleCleanUpIfIncomplete()
		} else {
			// executor is async
			c.performCleanUp(nil)
		}
	}
}

func (c *cache[K, V]) performCleanUp(t *task[K, V]) {
	c.evictionMutex.Lock()
	c.maintenanceAndUnlock(t)
	c.rescheduleCleanUpIfIncomplete()
}

// maintenanceAndUnlock runs maintenance with evictionMutex held by the caller and releases it,
// also when maintenance panics, so that a panic does not stop maintenance and every writer.
func (c *cache[K, V]) maintenanceAndUnlock(t *task[K, V]) {
	defer c.evictionMutex.Unlock()
	c.maintenance(t)
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
	c.readMaintenanceTime()
	completed := false
	defer func() {
		if !completed {
			// interrupted by a panic: the buffers may still hold work
			c.drainStatus.Store(required)
		}
	}()

	c.drainReadBuffer()
	c.drainWriteBuffer()
	c.runTask(t)
	c.expireNodes()
	c.evictNodes()
	c.climb()
	completed = true

	if c.drainStatus.Load() != processingToIdle || !c.drainStatus.CompareAndSwap(processingToIdle, idle) {
		c.drainStatus.Store(required)
	}
}

// readMaintenanceTime reads the time of a maintenance pass, under evictionMutex. Reading the clock
// once per pass rather than per eviction keeps it off the single-threaded maintenance path.
func (c *cache[K, V]) readMaintenanceTime() {
	if c.withExpiration {
		c.maintenanceNanos = c.clock.NowNano()
	}
}

func (c *cache[K, V]) drainReadBuffer() {
	if c.skipReadBuffer() {
		return
	}

	c.readBuffer.DrainTo(c.onAccess)
}

func (c *cache[K, V]) drainWriteBuffer() {
	if !c.withMaintenance {
		return
	}

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
		if c.withExpiration {
			c.timerWheel.Schedule(n)
		}
		if c.withEviction {
			c.evictionPolicy.add(n)
		}
	case updateReason:
		old := t.oldNode()
		if c.withExpiration {
			c.timerWheel.Unschedule(old)
			c.timerWheel.Schedule(n)
		}
		if c.withEviction {
			c.evictionPolicy.update(n, old)
		}
		c.notifyDeletion(old.Key(), old.Value(), t.deletionCause)
	case reconcileReason:
		// A weight or an expiration time changed in place. Both are brought in line with the
		// node's current state, so replaying the task late, twice or out of order is harmless.
		if c.withEviction {
			c.evictionPolicy.reweigh(n)
		}
		if c.withExpiration {
			c.timerWheel.Schedule(n)
		}
	case deleteReason:
		if c.withExpiration {
			c.timerWheel.Unschedule(n)
		}
		if c.withEviction {
			c.evictionPolicy.makeDead(n)
		}
		c.notifyDeletion(n.Key(), n.Value(), t.deletionCause)
	default:
		panic(fmt.Sprintf("Invalid task type: %d", t.writeReason))
	}
}

func (c *cache[K, V]) onAccess(n node.Node[K, V]) {
	if c.withEviction {
		c.evictionPolicy.access(n)
	}
	if c.withExpiration {
		c.timerWheel.Schedule(n)
	}
}

func (c *cache[K, V]) expireNodes() {
	if c.withExpiration {
		c.timerWheel.Advance(c.maintenanceNanos, c.expireNode)
	}
}

func (c *cache[K, V]) evictNodes() {
	if !c.withEviction {
		return
	}
	c.evictionPolicy.evictNodes()
}

func (c *cache[K, V]) climb() {
	if !c.withEviction {
		return
	}
	c.evictionPolicy.climb()
}

// newTask allocates a task. Tasks are not pooled: writers on every P would take them and
// maintenance on one P would return them, so writers would mostly steal from the pool of that P,
// which costs more than the allocation.
func newTask[K comparable, V any](n, old node.Node[K, V], writeReason reason, cause DeletionCause) *task[K, V] {
	return &task[K, V]{
		n:             n,
		old:           old,
		writeReason:   writeReason,
		deletionCause: cause,
	}
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
	c.evictionPolicy.setMaximumSize(maximum)
	c.maintenanceAndUnlock(nil)
	c.rescheduleCleanUpIfIncomplete()
}

// GetMaximum returns the maximum total weighted or unweighted size of this cache, depending on how the
// cache was constructed. If this cache does not use a (weighted) size bound, then the method will return math.MaxUint64.
func (c *cache[K, V]) GetMaximum() uint64 {
	if !c.withEviction {
		return uint64(math.MaxUint64)
	}

	result := func() uint64 {
		c.evictionMutex.Lock()
		defer c.evictionMutex.Unlock()
		if c.drainStatus.Load() == required {
			c.maintenance(nil)
		}
		return c.evictionPolicy.maximum
	}()
	c.rescheduleCleanUpIfIncomplete()
	return result
}

func (c *cache[K, V]) StopAllGoroutines() bool {
	var stopped bool
	c.stopOnce.Do(func() {
		if c.withExpiration {
			c.doneStop <- struct{}{}
			stopped = true
		}
	})
	return stopped
}

// EstimatedSize returns the approximate number of entries in this cache. The value returned is an estimate; the
// actual count may differ if there are concurrent insertions or deletions, or if some entries are
// pending deletion due to expiration. In the case of stale entries
// this inaccuracy can be mitigated by performing a CleanUp first.
func (c *cache[K, V]) EstimatedSize() int {
	return c.hashmap.Size()
}

// IsWeighted returns whether the cache is bounded by a maximum weight.
func (c *cache[K, V]) IsWeighted() bool {
	return c.isWeighted
}

// IsRecordingStats returns whether the cache statistics are being accumulated.
func (c *cache[K, V]) IsRecordingStats() bool {
	return c.withStats
}

// Stats returns a current snapshot of this cache's cumulative statistics.
// All statistics are initialized to zero and are monotonically increasing over the lifetime of the cache.
// Due to the performance penalty of maintaining statistics,
// some implementations may not record the usage history immediately or at all.
//
// NOTE: If your [stats.Recorder] implementation doesn't also implement [stats.Snapshoter],
// this method will always return a zero-value snapshot.
func (c *cache[K, V]) Stats() stats.Stats {
	return c.statsSnapshoter.Snapshot()
}

// WeightedSize returns the approximate accumulated weight of entries in this cache. If this cache does not
// use a weighted size bound, then the method will return 0.
func (c *cache[K, V]) WeightedSize() uint64 {
	if !c.isWeighted {
		return 0
	}

	result := func() uint64 {
		c.evictionMutex.Lock()
		defer c.evictionMutex.Unlock()
		if c.drainStatus.Load() == required {
			c.maintenance(nil)
		}
		return c.evictionPolicy.weightedSize
	}()
	c.rescheduleCleanUpIfIncomplete()
	return result
}

// Hottest returns an iterator for ordered traversal of the cache entries. The order of
// iteration is from the entries most likely to be retained (hottest) to the entries least
// likely to be retained (coldest). This order is determined by the eviction policy's best guess
// at the start of the iteration.
//
// WARNING: Beware that this iteration is performed within the eviction policy's exclusive lock, so the
// iteration should be short and simple. While the iteration is in progress further eviction
// maintenance will be halted.
func (c *cache[K, V]) Hottest() iter.Seq[Entry[K, V]] {
	return c.evictionOrder(true)
}

// Coldest returns an iterator for ordered traversal of the cache entries. The order of
// iteration is from the entries least likely to be retained (coldest) to the entries most
// likely to be retained (hottest). This order is determined by the eviction policy's best guess
// at the start of the iteration.
//
// WARNING: Beware that this iteration is performed within the eviction policy's exclusive lock, so the
// iteration should be short and simple. While the iteration is in progress further eviction
// maintenance will be halted.
func (c *cache[K, V]) Coldest() iter.Seq[Entry[K, V]] {
	return c.evictionOrder(false)
}

func (c *cache[K, V]) evictionOrder(hottest bool) iter.Seq[Entry[K, V]] {
	if !c.withEviction {
		return c.entries()
	}

	return func(yield func(Entry[K, V]) bool) {
		seq := c.evictionPolicy.coldest()
		if hottest {
			seq = c.evictionPolicy.hottest()
		}

		c.evictionMutex.Lock()
		defer c.evictionMutex.Unlock()
		c.maintenance(nil)

		for n := range seq {
			nowNano := c.clock.NowNano()
			if !n.IsAlive() || n.HasExpired(nowNano) {
				continue
			}
			if !yield(c.nodeToEntry(n, nowNano)) {
				return
			}
		}
	}
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
		c.evictionPolicy.makeDead(n)
	} else if !n.IsDead() {
		n.Die()
	}
}

func getCause[K comparable, V any](n node.Node[K, V], nowNano int64, cause DeletionCause) DeletionCause {
	if n.HasExpired(nowNano) {
		return CauseExpiration
	}
	return cause
}
