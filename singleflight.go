// Copyright (c) 2024 Alexey Mayshev and contributors. All rights reserved.
// Copyright 2009 The Go Authors. All rights reserved.
//
// Copyright notice. Initial version of the following code was based on
// the following file from the Go Programming Language core repo:
// https://cs.opensource.google/go/go/+/refs/tags/go1.21.5:src/container/list/list_test.go
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// That can be found at https://cs.opensource.google/go/go/+/refs/tags/go1.21.5:LICENSE

package otter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/hashmap"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

// call is a load or refresh of one key that its waiters share. Its fields are written only
// in this file: by its registration, by the load, and by group.finish.
type call[K comparable, V any] struct {
	key   K
	value V
	err   error
	// base is the node a refresh was requested for (nil for a load or an absent key). The
	// call's result is written only if base is still the key's node.
	base node.Node[K, V]
	// done is closed when the call has finished and its result may be read.
	done       chan struct{}
	isRefresh  bool
	isNotFound bool
	isFake     bool
	// ownerCtxDone reports that the context the loader ran with had ended by the time it
	// returned.
	ownerCtxDone bool
	// committed reports that the cache has decided what the call writes. It is written and
	// read under the key's bucket lock in the cache's hash table.
	committed bool
	// refused reports that the cache has not written the value a refresh or a load returned:
	// the call was cancelled, or a refresh's node was replaced or expired. It is written
	// under the key's bucket lock, before done is closed.
	refused bool
	// expiresAt is when the value that the call hands to its waiters from the cache, the one it
	// wrote or the live one it found, expires, and node is the node that held the value then;
	// expiresAt is 0 if the call hands none or entries do not expire. A waiter whose Get started
	// after the value expired does not take it (expiredBefore). Both are written before done is
	// closed.
	expiresAt int64
	node      node.Node[K, V]
	// invalidations is group.invalidations when the bulk load that returned a fake call started.
	invalidations uint64
}

func newCall[K comparable, V any](key K, isRefresh bool, base node.Node[K, V]) *call[K, V] {
	c := &call[K, V]{
		key:       key,
		isRefresh: isRefresh,
		base:      base,
		done:      make(chan struct{}),
	}
	return c
}

func (c *call[K, V]) Key() K {
	return c.key
}

func (c *call[K, V]) Value() V {
	return c.value
}

func (c *call[K, V]) AsPointer() unsafe.Pointer {
	//nolint:gosec // it's ok
	return unsafe.Pointer(c)
}

func (c *call[K, V]) cancel() {
	if c.isFake {
		return
	}
	close(c.done)
}

func (c *call[K, V]) wait() {
	<-c.done
}

// waitContext waits for c to finish or for ctx to end, and returns ctx's error in the latter
// case. A call that has finished by then wins, so its result is not lost to a context that
// ended at the same moment.
func (c *call[K, V]) waitContext(ctx context.Context) error {
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		select {
		case <-c.done:
			return nil
		default:
			return ctx.Err()
		}
	}
}

// failedForOwner reports whether c, which has finished, failed after the context of the caller
// that ran it had ended. The error then most likely comes from that caller's cancellation or
// deadline, which says nothing about the key, so a waiter with a live context loads the key
// again rather than return it. Not every loader wraps context.Canceled into its error (an RPC
// client may return its own status), so any failure counts, except the ones that come from the
// key itself: not found, a panic and runtime.Goexit.
func (c *call[K, V]) failedForOwner() bool {
	if !c.ownerCtxDone || c.err == nil || c.isNotFound || errors.Is(c.err, errLoaderExited) {
		return false
	}
	var pe *panicError
	return !errors.As(c.err, &pe)
}

// expiredBefore reports whether c, which has finished, hands its waiters a value from the cache
// that had expired by nowNano. The value stays in the call until the call is deleted, and
// SetExpiresAfter or ExpireAfterRead may move its deadline earlier meanwhile, so the node's
// current deadline counts as well as the recorded one. The node may hold a later value by then,
// whose deadline can only make a waiter miss and load the key again.
func (c *call[K, V]) expiredBefore(nowNano int64) bool {
	return c.expiresAt != 0 && (c.expiresAt <= nowNano || c.node.ExpiresAt() <= nowNano)
}

// refreshResult is what a manual refresh reports for c once it has finished.
func (c *call[K, V]) refreshResult() RefreshResult[K, V] {
	return RefreshResult[K, V]{
		Key:   c.key,
		Value: c.value,
		Err:   c.err,
	}
}

type mapCallManager[K comparable, V any] struct{}

func (m *mapCallManager[K, V]) FromPointer(ptr unsafe.Pointer) *call[K, V] {
	return (*call[K, V])(ptr)
}

// group runs loads and refreshes so that each key has at most one registered at a time (a
// superseded refresh keeps running for the callers that joined it), and owns their lifecycle: a call is registered before it runs, runs with its panics and runtime.Goexit
// caught, and is finished exactly once, whatever fails: its result is committed, then the call
// is deleted and its waiters woken, then the commit is published.
type group[K comparable, V any] struct {
	calls         *hashmap.Map[K, V, *call[K, V]]
	initMutex     sync.Mutex
	isInitialized atomic.Bool

	// commit decides and applies under the key's bucket lock what a finished call writes. It
	// asks authoritative whether the call's result may be written.
	commit func(cl *call[K, V]) mutation[K, V]
	// publish hands a committed mutation to the policies and listeners.
	publish func(m mutation[K, V])
	// executor runs refresh tasks.
	executor func(fn func())
	// nodeOf returns the key's node in the cache's hash table, or nil.
	nodeOf func(key K) node.Node[K, V]

	// The counters below change on bulk loads and invalidations, while the fields above are read
	// on every Get and write: they are kept off their cache line.
	_ [xruntime.CacheLineSize]byte
	// bulkLoads is the number of bulk loads in flight, and invalidations counts the invalidations
	// made while there was one, so that a fake call is not written over an invalidation that
	// followed the start of its load (see authoritative).
	bulkLoads     atomic.Int64
	invalidations atomic.Uint64
}

func (g *group[K, V]) init() {
	if !g.isInitialized.Load() {
		g.initMutex.Lock()
		if !g.isInitialized.Load() {
			g.calls = hashmap.New[K, V, *call[K, V]](&mapCallManager[K, V]{})
			g.isInitialized.Store(true)
		}
		g.initMutex.Unlock()
	}
}

func (g *group[K, V]) getCall(key K) *call[K, V] {
	return g.calls.Get(key)
}

// startLoad registers a load of key, or returns the call already registered for it. shouldLoad
// reports that the caller registered the call and must run it.
func (g *group[K, V]) startLoad(key K) (c *call[K, V], shouldLoad bool) {
	return g.startCall(key, false, nil)
}

// startRefresh registers a refresh of key, whose node was base (nil if absent) when it was
// requested, or returns the call already registered for it. A refresh is registered on the
// requesting goroutine, before it is handed to the executor, so that a write that follows the
// request cancels it.
func (g *group[K, V]) startRefresh(key K, base node.Node[K, V]) (c *call[K, V], shouldLoad bool) {
	return g.startCall(key, true, base)
}

func (g *group[K, V]) startCall(key K, isRefresh bool, base node.Node[K, V]) (c *call[K, V], shouldLoad bool) {
	// fast path
	if c := g.getCall(key); c != nil && !g.superseded(c) {
		return c, shouldLoad
	}

	return g.calls.Compute(key, func(prevCall *call[K, V]) *call[K, V] {
		// double check
		if prevCall != nil && !g.superseded(prevCall) {
			return prevCall
		}
		shouldLoad = true
		return newCall[K, V](key, isRefresh, base)
	}), shouldLoad
}

// superseded reports whether c is a refresh whose base has been removed or replaced, so that its
// result will not be written (see isRefreshBase). Such a refresh was registered after the write
// or removal that would have cancelled it. The next call for its key replaces it rather than join
// it: a Get after an invalidation would get the reload of the invalidated value, and a stale node
// that replaced the base would not be refreshed until this refresh has finished. The refresh still
// completes for the callers that joined it before. A removed or replaced node never comes back, so
// a refresh that is superseded stays so.
func (g *group[K, V]) superseded(c *call[K, V]) bool {
	return c.isRefresh && c.base != nil && (!c.base.IsAlive() || g.nodeOf(c.key) != c.base)
}

// adopt finishes c, which the caller registered, with a value that is already in the cache,
// without loading it. The value, held by n, expires at expiresAt (see call.expiresAt).
func (g *group[K, V]) adopt(c *call[K, V], value V, expiresAt int64, n node.Node[K, V]) {
	c.value = value
	c.expiresAt, c.node = expiresAt, n
	g.deleteCall(c)
	c.cancel()
}

// doCall runs load for c, which the caller registered, and finishes c whatever happens, with a
// panic or runtime.Goexit of load as its error. Everything that can panic runs after the defer
// that finishes c: a panic between the registration and that defer would leave c registered,
// and every later load of its key would wait for it. That includes detaching a refresh from the
// cancellation of the request (a refresh outlives the read that asked for it), which panics on
// a nil context, and the loader's method, which callers pass in a closure rather than as a method
// value, since the method value of a nil loader panics where it is taken.
func (g *group[K, V]) doCall(
	ctx context.Context,
	c *call[K, V],
	load func(ctx context.Context, key K) (V, error),
) (err error) {
	returned := false
	defer func() {
		if r := recover(); r != nil {
			err = newPanicError(r)
		} else if !returned {
			// The loader called runtime.Goexit: there is no value, and the waiters must not
			// take the zero value for one.
			err = errLoaderExited
		}

		c.err = err
		c.isNotFound = errors.Is(err, ErrNotFound)
		g.finish(c)
	}()

	if c.isRefresh {
		ctx = context.WithoutCancel(ctx)
	}
	c.value, err = load(ctx, c.key)
	returned = true
	c.ownerCtxDone = ctx.Err() != nil
	return err
}

// doBulkCall is doCall for the calls of a bulk load, all loads or all refreshes. It also adds a
// fake call for each key the loader returned without being asked for.
func (g *group[K, V]) doBulkCall(
	ctx context.Context,
	callsInBulk map[K]*call[K, V],
	bulkLoad func(ctx context.Context, keys []K) (map[K]V, error),
) (err error) {
	// Counted before the invalidations are read, and until the calls are finished. An invalidation
	// that does not see this load came before it, so the load reads the data source after the
	// change that the invalidation follows.
	g.bulkLoads.Add(1)
	defer g.bulkLoads.Add(-1)
	invalidations := g.invalidations.Load()

	returned := false
	defer func() {
		if r := recover(); r != nil {
			err = newPanicError(r)
		} else if !returned {
			err = errLoaderExited
		}

		if err != nil {
			// A key is not found only if the loader left it out of a result: an ErrNotFound
			// for the whole bulk does not remove the keys.
			for _, cl := range callsInBulk {
				cl.err = err
				cl.isNotFound = false
			}
		}

		// Every call is finished, even if finishing one of them panics: an unfinished call
		// would block its waiters and every later load of its key.
		var finishPanic any
		for _, cl := range callsInBulk {
			func() {
				defer func() {
					if r := recover(); r != nil && finishPanic == nil {
						finishPanic = r
					}
				}()
				g.finish(cl)
			}()
		}
		if finishPanic != nil {
			panic(finishPanic)
		}
	}()

	keys := make([]K, 0, len(callsInBulk))
	isRefresh := false
	for k, cl := range callsInBulk {
		keys = append(keys, k)
		isRefresh = cl.isRefresh
	}

	if isRefresh {
		ctx = context.WithoutCancel(ctx)
	}
	res, err := bulkLoad(ctx, keys)
	returned = true
	ownerCtxDone := ctx.Err() != nil

	for k, cl := range callsInBulk {
		cl.ownerCtxDone = ownerCtxDone
		v, ok := res[k]
		if ok {
			cl.value = v
		} else {
			// A key the loader left out is not found, just as if Loader.Load had returned
			// ErrNotFound: without the error, every reader would take the zero value for a
			// loaded one.
			cl.err = ErrNotFound
			cl.isNotFound = true
		}
	}

	for k, v := range res {
		if _, ok := callsInBulk[k]; ok {
			continue
		}
		callsInBulk[k] = &call[K, V]{
			key:           k,
			value:         v,
			isFake:        true,
			isRefresh:     isRefresh,
			invalidations: invalidations,
		}
	}

	return err
}

// finish commits the result of c, then deletes c, wakes its waiters and publishes the commit.
// Deleting c only after the commit has published the value closes a window in which a new
// load of the key could start before the value is visible. If committing panics (the weigher
// or a calculator) or calls runtime.Goexit, c is still deleted and its waiters are woken, with
// the panic as its error: left registered, it would block every later load of its key.
func (g *group[K, V]) finish(c *call[K, V]) {
	finished := false
	defer func() {
		if finished {
			return
		}
		if !c.isFake {
			g.deleteCall(c)
		}
		if r := recover(); r != nil {
			c.err = newPanicError(r)
			c.cancel()
			panic(r)
		}
		c.cancel()
	}()

	m := g.commit(c)
	if !c.isFake {
		g.deleteCall(c)
	}
	finished = true
	c.cancel()
	g.publish(m)
}

// authoritative reports whether the result of c may be written over prev, the key's entry when
// c finishes. It runs under the key's bucket lock, before c is deleted.
//
// A call is stale once a write or an invalidation has deleted it. It is also stale if its base
// node was replaced, removed or has expired in the meantime, so a load, whose base is nil, never
// writes over a live value. A key the bulk loader returned without being asked for was not
// registered before the load, so a write during the load could not cancel it: it only fills a
// gap, where the key has no live value and no load of its own, and only if no key has been
// invalidated since the load started, since the value may have been read from the data source
// before the invalidation.
func (g *group[K, V]) authoritative(c *call[K, V], prev previous[K, V], nowNano int64) bool {
	if c.isFake {
		return !prev.live && g.getCall(c.key) == nil && g.invalidations.Load() == c.invalidations
	}
	return g.getCall(c.key) == c && isRefreshBase(c.base, prev.node, nowNano)
}

// isRefreshBase reports whether current, the key's node when a refresh completes, is still the
// node base that the refresh was requested for. A refresh of an absent key (base == nil) also
// matches an expired node, which the request did not see. A base that has expired since the
// request is absent, so there is nothing left to refresh.
func isRefreshBase[K comparable, V any](base, current node.Node[K, V], nowNano int64) bool {
	if base != nil {
		return current != nil && current == base && !current.HasExpired(nowNano)
	}
	return current == nil || current.HasExpired(nowNano)
}

// submitRefresh submits a refresh task that finishes the calls in owned, which the caller
// registered. If the executor panics, the calls are finished here with the panic as their error:
// left registered, they would block every later load of their keys. The task is then abandoned,
// so that an executor that scheduled it before panicking does not finish the calls again.
func (g *group[K, V]) submitRefresh(owned []*call[K, V], task func()) {
	const (
		submitted = iota
		started
		abandoned
	)
	var state atomic.Int32
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if state.CompareAndSwap(submitted, abandoned) {
			err := newPanicError(r)
			for _, cl := range owned {
				cl.err = err
				g.deleteCall(cl)
				cl.cancel()
			}
		}
		panic(r)
	}()
	g.executor(func() {
		if !state.CompareAndSwap(submitted, started) {
			return
		}
		task()
	})
}

func (g *group[K, V]) deleteCall(c *call[K, V]) (deleted bool) {
	// fast path
	if got := g.getCall(c.key); got != c {
		return false
	}

	cl := g.calls.Compute(c.key, func(prevCall *call[K, V]) *call[K, V] {
		// double check
		if prevCall == c {
			// delete
			return nil
		}
		return prevCall
	})
	return cl == nil
}

// delete removes the call registered for key, if any. Every write and invalidation of a cache
// that has loaded at least once comes here, and there is rarely a call to remove, so the lock-free
// lookup skips taking the bucket lock. A call registered concurrently can be missed by the lookup
// just as by Compute, which sees the bucket at one moment too.
// invalidate deletes the call registered for key, as delete does, for an invalidation of key.
// While a bulk load is in flight, it also counts the invalidation, which keeps the fake calls of
// that load from being written. It is called under the key's bucket lock in the cache's hash
// table, as is authoritative for a fake call of key.
func (g *group[K, V]) invalidate(key K) {
	if g.bulkLoads.Load() > 0 {
		g.invalidations.Add(1)
	}
	g.delete(key)
}

func (g *group[K, V]) delete(key K) {
	if !g.isInitialized.Load() || g.getCall(key) == nil {
		return
	}

	g.calls.Compute(key, func(prevCall *call[K, V]) *call[K, V] {
		return nil
	})
}

// deleteOnRemoval removes the call registered for key when the key's entry is evicted or expires,
// unless it is a load still in flight: a refresh of an existing node, or a call that has already
// committed its value. A committed call stays registered until it finishes, and a Get that
// joined it after the removal would get back the value just removed. An in-flight load is kept:
// it was most likely started because this very entry has expired. So is a refresh of an absent
// key, which loads it in the same way: an expired entry that was still in the table when the
// refresh was requested is not a value of the key.
//
// It is called under the key's bucket lock in the cache's hash table, which guards committed.
// Every eviction comes here, so as in delete a lookup without the lock comes first; isRefresh and
// base are set before the call is registered and never change.
func (g *group[K, V]) deleteOnRemoval(key K) {
	if !g.isInitialized.Load() {
		return
	}
	if cl := g.getCall(key); cl == nil || !droppedOnRemoval(cl) {
		return
	}

	g.calls.Compute(key, func(prevCall *call[K, V]) *call[K, V] {
		if prevCall != nil && droppedOnRemoval(prevCall) {
			return nil
		}
		return prevCall
	})
}

func droppedOnRemoval[K comparable, V any](c *call[K, V]) bool {
	return (c.isRefresh && c.base != nil) || c.committed
}
