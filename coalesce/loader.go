// Copyright (c) 2026 Alexey Mayshev and contributors. All rights reserved.
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

package coalesce

import (
	"context"
	"errors"
	"sync"

	"github.com/maypok86/otter/v2"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

// errLoaderExited is the error of a batch whose bulk loader called runtime.Goexit.
var errLoaderExited = errors.New("coalesce: the bulk loader exited the goroutine (runtime.Goexit) without returning")

// Loader collects the keys that concurrent calls of Load and Reload ask for and loads them with
// one call of BulkLoad or BulkReload of its [otter.BulkLoader].
//
// A key waits in a queue until a call of the bulk loader may start (see [Options.MaxInFlight]);
// the keys that are waiting then leave together, at most [Options.MaxBatchSize] at a time. A key
// that several calls wait for is loaded once; for Reload, with the old value of the first call.
// Loads and reloads go in separate batches that share the calls of the bulk loader, and when
// both are waiting, they take turns.
//
// A batch runs with the values of the context of its first call, but not with its cancellation:
// the batch is cancelled only when every call waiting for it has returned. Each call returns as
// soon as its own context is done. A key whose callers have all returned before its batch leaves
// is not loaded.
//
// A call returns [otter.ErrNotFound] for a key that the bulk loader leaves out of its result, and
// the error of the bulk loader for every key of a failed batch. If the bulk loader panics, every
// call waiting for the batch panics with the same value; if no call waits any more, the panic is
// logged with [Options.Logger]. The keys that the bulk loader returns without being asked for are
// dropped.
type Loader[K comparable, V any] struct {
	bulkLoader   otter.BulkLoader[K, V]
	maxBatchSize int
	maxInFlight  int
	logger       otter.Logger

	mu      sync.Mutex
	loads   queue[K, V]
	reloads queue[K, V]
	// reloadedLast reports that the last batch started was a batch of reloads.
	reloadedLast bool
	inFlight     int
}

// queue holds the requests of one kind waiting for a batch, in the order they came. A request
// whose callers have all returned is left in it as abandoned and skipped.
type queue[K comparable, V any] struct {
	isReload bool
	requests []*request[K, V]
	// waiting maps the key of every request in requests that is not abandoned to it.
	waiting map[K]*request[K, V]
}

// request is the load or reload of one key that one or more calls wait for.
type request[K comparable, V any] struct {
	key      K
	oldValue V
	// ctx is the context of the call that queued the request.
	ctx context.Context
	// done is closed when the result below may be read.
	done chan struct{}

	value V
	found bool
	err   error
	// panicVal is the value the bulk loader panicked with. It is never nil for a panic: since
	// Go 1.21, panic(nil) is recovered as a *runtime.PanicNilError.
	panicVal any

	// The fields below are guarded by Loader.mu.

	// queue is the queue of the request.
	queue *queue[K, V]
	// callers is the number of calls still waiting for the request.
	callers int
	// batch is the batch that loads the request, nil while it is queued.
	batch *batch
}

// batch is one call of the bulk loader.
type batch struct {
	// live is the number of its requests that some call still waits for. It is guarded by
	// Loader.mu.
	live   int
	cancel context.CancelFunc
}

// New creates a [Loader] that loads keys with bulkLoader, or returns an error if the options
// are invalid. A nil o is the same as the zero [Options].
func New[K comparable, V any](bulkLoader otter.BulkLoader[K, V], o *Options) (*Loader[K, V], error) {
	if bulkLoader == nil {
		return nil, errors.New("coalesce: bulkLoader should not be nil")
	}
	if o == nil {
		o = &Options{}
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	return &Loader[K, V]{
		bulkLoader:   bulkLoader,
		maxBatchSize: o.MaxBatchSize,
		maxInFlight:  o.getMaxInFlight(),
		logger:       o.getLogger(),
		loads:        queue[K, V]{waiting: make(map[K]*request[K, V])},
		reloads:      queue[K, V]{isReload: true, waiting: make(map[K]*request[K, V])},
	}, nil
}

// Must creates a [Loader] like [New] and panics if the options are invalid.
func Must[K comparable, V any](bulkLoader otter.BulkLoader[K, V], o *Options) *Loader[K, V] {
	l, err := New(bulkLoader, o)
	if err != nil {
		panic(err)
	}
	return l
}

// Load loads the value for key with BulkLoad, in a batch with the keys of other concurrent calls.
func (l *Loader[K, V]) Load(ctx context.Context, key K) (V, error) {
	return l.load(ctx, &l.loads, key, zeroValue[V]())
}

// Reload loads a replacement value for key with BulkReload, in a batch with the keys of other
// concurrent calls.
func (l *Loader[K, V]) Reload(ctx context.Context, key K, oldValue V) (V, error) {
	return l.load(ctx, &l.reloads, key, oldValue)
}

// load queues key in q, or joins the request already queued for it, and waits for the result.
func (l *Loader[K, V]) load(ctx context.Context, q *queue[K, V], key K, oldValue V) (V, error) {
	if err := ctx.Err(); err != nil {
		return zeroValue[V](), err
	}

	l.mu.Lock()
	r := q.waiting[key]
	if r == nil {
		r = &request[K, V]{
			key:      key,
			oldValue: oldValue,
			ctx:      ctx,
			done:     make(chan struct{}),
			queue:    q,
		}
		q.requests = append(q.requests, r)
		q.waiting[key] = r
	}
	r.callers++
	l.dispatch()
	l.mu.Unlock()

	select {
	case <-r.done:
	case <-ctx.Done():
		// A request that has finished by then wins, so its result is not lost to a context
		// that ended at the same moment.
		select {
		case <-r.done:
		default:
			l.leave(r)
			return zeroValue[V](), ctx.Err()
		}
	}

	if r.panicVal != nil {
		panic(r.panicVal)
	}
	if r.err != nil {
		return zeroValue[V](), r.err
	}
	if !r.found {
		return zeroValue[V](), otter.ErrNotFound
	}
	return r.value, nil
}

func zeroValue[V any]() V {
	var v V
	return v
}

// leave unregisters a call that stops waiting for r. A queued request that nobody waits for is
// abandoned, and a batch that nobody waits for is cancelled.
func (l *Loader[K, V]) leave(r *request[K, V]) {
	l.mu.Lock()
	defer l.mu.Unlock()

	r.callers--
	if r.callers > 0 {
		return
	}
	if r.batch == nil {
		delete(r.queue.waiting, r.key)
		r.queue.compact()
		return
	}
	r.batch.live--
	if r.batch.live == 0 {
		r.batch.cancel()
	}
}

// compact drops the abandoned requests from q once they outnumber the live ones. The queue is
// cleaned up otherwise only when a batch starts, so while the bulk loader hangs, the callers that
// give up would pile up in it with their contexts. It is called with Loader.mu held.
func (q *queue[K, V]) compact() {
	if len(q.requests) <= 2*len(q.waiting)+16 {
		return
	}
	live := make([]*request[K, V], 0, len(q.waiting))
	for _, r := range q.requests {
		if q.waiting[r.key] == r {
			live = append(live, r)
		}
	}
	q.requests = live
}

// take removes the first live requests from q, at most maxBatchSize of them if it is positive.
// It is called with Loader.mu held.
func (q *queue[K, V]) take(maxBatchSize int) []*request[K, V] {
	size := len(q.waiting)
	if maxBatchSize > 0 && size > maxBatchSize {
		size = maxBatchSize
	}
	requests := make([]*request[K, V], 0, size)
	taken := 0
	for _, r := range q.requests {
		if len(requests) == size {
			break
		}
		taken++
		if q.waiting[r.key] != r {
			// abandoned
			continue
		}
		delete(q.waiting, r.key)
		requests = append(requests, r)
	}
	clear(q.requests[:taken])
	q.requests = q.requests[taken:]
	if len(q.requests) == 0 {
		q.requests = nil
	}
	return requests
}

// next returns the queue to take the next batch from, or nil if both are empty. When both have
// keys, they take turns, so that a steady stream of loads does not starve the reloads, nor the
// other way round. It is called with l.mu held.
func (l *Loader[K, V]) next() *queue[K, V] {
	hasLoads, hasReloads := len(l.loads.waiting) > 0, len(l.reloads.waiting) > 0
	switch {
	case hasLoads && hasReloads:
		if l.reloadedLast {
			return &l.loads
		}
		return &l.reloads
	case hasLoads:
		return &l.loads
	case hasReloads:
		return &l.reloads
	default:
		return nil
	}
}

// dispatch starts batches of the queued requests while a call of the bulk loader may start. It
// is called with l.mu held.
func (l *Loader[K, V]) dispatch() {
	for l.maxInFlight < 0 || l.inFlight < l.maxInFlight {
		q := l.next()
		if q == nil {
			return
		}
		requests := q.take(l.maxBatchSize)
		l.reloadedLast = q.isReload

		ctx, cancel := context.WithCancel(context.WithoutCancel(requests[0].ctx))
		b := &batch{live: len(requests), cancel: cancel}
		for _, r := range requests {
			r.batch = b
		}
		l.inFlight++
		go l.run(ctx, b, q.isReload, requests)
	}
}

// run loads the keys of requests with one call of the bulk loader, starts the next batch and
// wakes the callers.
func (l *Loader[K, V]) run(ctx context.Context, b *batch, isReload bool, requests []*request[K, V]) {
	keys := make([]K, len(requests))
	for i, r := range requests {
		keys[i] = r.key
	}

	returned := false
	defer func() {
		p := recover()
		var panicErr error
		if p != nil {
			// Taken here, while the stack still holds the frames of the panic.
			panicErr = xruntime.NewPanicError(p)
		}
		for _, r := range requests {
			if p != nil {
				r.panicVal = p
			} else if !returned {
				r.err = errLoaderExited
			}
		}

		// The next batch starts before the callers wake up, so that a caller that loads again
		// at once finds the call free.
		l.mu.Lock()
		l.inFlight--
		l.dispatch()
		unseen := p != nil && b.live == 0
		l.mu.Unlock()

		for _, r := range requests {
			close(r.done)
		}
		b.cancel()
		if unseen {
			// No caller is left to panic with p. Panicking here would crash the process, where
			// the caller's goroutine might have recovered, and dropping p would hide the bug.
			l.logger.Error(ctx, "The bulk loader panicked with no call waiting for it", panicErr)
		}
	}()

	var (
		res map[K]V
		err error
	)
	if isReload {
		oldValues := make([]V, len(requests))
		for i, r := range requests {
			oldValues[i] = r.oldValue
		}
		res, err = l.bulkLoader.BulkReload(ctx, keys, oldValues)
	} else {
		res, err = l.bulkLoader.BulkLoad(ctx, keys)
	}
	returned = true
	for _, r := range requests {
		if err != nil {
			r.err = err
			continue
		}
		r.value, r.found = res[r.key]
	}
}
