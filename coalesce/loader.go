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

// Loader collects the keys that concurrent calls of Load ask for and loads them with one call of
// BulkLoad of its [otter.BulkLoader].
//
// A key waits in a queue until a call of BulkLoad may start (see [Options.MaxInFlight]); the
// keys that are waiting then leave together, at most [Options.MaxBatchSize] at a time. A key
// that several calls wait for is loaded once.
//
// BulkLoad runs with the values of the context of the first call in the batch, but not with its
// cancellation: the batch is cancelled only when every call waiting for it has returned. Each
// call of Load returns as soon as its own context is done. A key whose callers have all
// returned before its batch leaves is not loaded.
//
// Load returns [otter.ErrNotFound] for a key that BulkLoad leaves out of its result, and the error
// of BulkLoad for every key of a failed batch. If BulkLoad panics, every call of Load waiting for
// the batch panics with the same value; if no call waits any more, the panic is logged with
// [Options.Logger]. The keys that BulkLoad returns without being asked for are dropped.
type Loader[K comparable, V any] struct {
	bulkLoader   otter.BulkLoader[K, V]
	maxBatchSize int
	maxInFlight  int
	logger       otter.Logger

	mu sync.Mutex
	// queue holds the requests waiting for a batch, in the order they came. A request whose
	// callers have all returned is left in it as abandoned and skipped.
	queue []*request[K, V]
	// waiting maps the key of every request in queue that is not abandoned to it.
	waiting  map[K]*request[K, V]
	inFlight int
}

// request is the load of one key that one or more calls of Load wait for.
type request[K comparable, V any] struct {
	key K
	// ctx is the context of the call that queued the request.
	ctx context.Context
	// done is closed when the result below may be read.
	done chan struct{}

	value V
	found bool
	err   error
	// panicVal is the value BulkLoad panicked with. It is never nil for a panic: since Go 1.21,
	// panic(nil) is recovered as a *runtime.PanicNilError.
	panicVal any

	// The fields below are guarded by Loader.mu.

	// callers is the number of calls of Load still waiting for the request.
	callers int
	// batch is the batch that loads the request, nil while it is queued.
	batch *batch
}

// batch is one call of BulkLoad.
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
		waiting:      make(map[K]*request[K, V]),
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

// Load loads the value for key in a batch with the keys of other concurrent calls.
func (l *Loader[K, V]) Load(ctx context.Context, key K) (V, error) {
	if err := ctx.Err(); err != nil {
		return zeroValue[V](), err
	}

	l.mu.Lock()
	r := l.waiting[key]
	if r == nil {
		r = &request[K, V]{
			key:  key,
			ctx:  ctx,
			done: make(chan struct{}),
		}
		l.queue = append(l.queue, r)
		l.waiting[key] = r
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

// Reload loads a replacement value for key with BulkReload. It is not batched yet.
func (l *Loader[K, V]) Reload(ctx context.Context, key K, oldValue V) (V, error) {
	res, err := l.bulkLoader.BulkReload(ctx, []K{key}, []V{oldValue})
	if err != nil {
		return zeroValue[V](), err
	}
	v, ok := res[key]
	if !ok {
		return zeroValue[V](), otter.ErrNotFound
	}
	return v, nil
}

func zeroValue[V any]() V {
	var v V
	return v
}

// leave unregisters a call of Load that stops waiting for r. A queued request that nobody waits
// for is abandoned, and a batch that nobody waits for is cancelled.
func (l *Loader[K, V]) leave(r *request[K, V]) {
	l.mu.Lock()
	defer l.mu.Unlock()

	r.callers--
	if r.callers > 0 {
		return
	}
	if r.batch == nil {
		delete(l.waiting, r.key)
		l.compactQueue()
		return
	}
	r.batch.live--
	if r.batch.live == 0 {
		r.batch.cancel()
	}
}

// compactQueue drops the abandoned requests from the queue once they outnumber the live ones.
// The queue is cleaned up otherwise only when a batch starts, so while BulkLoad hangs, the callers
// that give up would pile up in it with their contexts. It is called with l.mu held.
func (l *Loader[K, V]) compactQueue() {
	if len(l.queue) <= 2*len(l.waiting)+16 {
		return
	}
	live := make([]*request[K, V], 0, len(l.waiting))
	for _, r := range l.queue {
		if l.waiting[r.key] == r {
			live = append(live, r)
		}
	}
	l.queue = live
}

// dispatch starts batches of the queued requests while a call of BulkLoad may start. It is
// called with l.mu held.
func (l *Loader[K, V]) dispatch() {
	for len(l.waiting) > 0 && (l.maxInFlight < 0 || l.inFlight < l.maxInFlight) {
		size := len(l.waiting)
		if l.maxBatchSize > 0 && size > l.maxBatchSize {
			size = l.maxBatchSize
		}
		requests := make([]*request[K, V], 0, size)
		taken := 0
		for _, r := range l.queue {
			if len(requests) == size {
				break
			}
			taken++
			if l.waiting[r.key] != r {
				// abandoned
				continue
			}
			delete(l.waiting, r.key)
			requests = append(requests, r)
		}
		clear(l.queue[:taken])
		l.queue = l.queue[taken:]
		if len(l.queue) == 0 {
			l.queue = nil
		}

		ctx, cancel := context.WithCancel(context.WithoutCancel(requests[0].ctx))
		b := &batch{live: len(requests), cancel: cancel}
		for _, r := range requests {
			r.batch = b
		}
		l.inFlight++
		go l.run(ctx, b, requests)
	}
}

// run loads the keys of requests with one call of BulkLoad, starts the next batch and wakes the
// callers.
func (l *Loader[K, V]) run(ctx context.Context, b *batch, requests []*request[K, V]) {
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
			l.logger.Error(ctx, "BulkLoad panicked with no call of Load waiting for it", panicErr)
		}
	}()

	res, err := l.bulkLoader.BulkLoad(ctx, keys)
	returned = true
	for _, r := range requests {
		if err != nil {
			r.err = err
			continue
		}
		r.value, r.found = res[r.key]
	}
}
