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
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/maypok86/otter/v2/internal/hashmap"
)

// errGoexit indicates the runtime.Goexit was called in
// the user given function.
var errGoexit = errors.New("runtime.Goexit was called")

// A panicError is an arbitrary value recovered from a panic
// with the stack trace during the execution of given function.
type panicError struct {
	value any
	stack []byte
}

// Error implements error interface.
func (p *panicError) Error() string {
	return fmt.Sprintf("%v\n\n%s", p.value, p.stack)
}

func (p *panicError) Unwrap() error {
	err, ok := p.value.(error)
	if !ok {
		return nil
	}

	return err
}

func newPanicError(v any) error {
	stack := debug.Stack()

	// The first line of the stack trace is of the form "goroutine N [status]:"
	// but by the time the panic reaches Do the goroutine may no longer exist
	// and its status will have changed. Trim out the misleading line.
	if line := bytes.IndexByte(stack, '\n'); line >= 0 {
		stack = stack[line+1:]
	}
	return &panicError{value: v, stack: stack}
}

type call[K comparable, V any] struct {
	key    K
	value  V
	err    error
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func newCall[K comparable, V any](ctx context.Context, key K) *call[K, V] {
	ctx, cancel := context.WithCancel(ctx)
	c := &call[K, V]{
		key:    key,
		ctx:    ctx,
		cancel: cancel,
	}
	c.wg.Add(1)
	return c
}

func (c *call[K, V]) Key() K {
	return c.key
}

func (c *call[K, V]) Value() V {
	return c.value
}

func (c *call[K, V]) AsPointer() unsafe.Pointer {
	return unsafe.Pointer(c)
}

func (c *call[K, V]) wait() {
	c.wg.Wait()
}

type mapCallManager[K comparable, V any] struct{}

func (m *mapCallManager[K, V]) FromPointer(ptr unsafe.Pointer) *call[K, V] {
	return (*call[K, V])(ptr)
}

func (m *mapCallManager[K, V]) IsNil(c *call[K, V]) bool {
	return c == nil
}

type group[K comparable, V any] struct {
	calls         *hashmap.Map[K, V, *call[K, V]]
	initMutex     sync.Mutex
	isInitialized atomic.Bool
}

func (g *group[K, V]) lazyInit() {
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

func (g *group[K, V]) startCall(ctx context.Context, key K) (c *call[K, V], shouldDo bool) {
	// fast path
	if c := g.getCall(key); c != nil {
		return c, shouldDo
	}

	return g.calls.Compute(key, func(prevCall *call[K, V]) *call[K, V] {
		// double check
		if prevCall != nil {
			return prevCall
		}
		shouldDo = true
		return newCall[K, V](ctx, key)
	}), shouldDo
}

func (g *group[K, V]) doCall(c *call[K, V], loader Loader[K, V], afterFinish func(c *call[K, V])) {
	normalReturn := false
	recovered := false

	// use double-defer to distinguish panic from runtime.Goexit,
	// more details see https://golang.org/cl/134395
	defer func() {
		// the given function invoked runtime.Goexit
		if !normalReturn && !recovered {
			c.err = errGoexit
		}

		afterFinish(c)

		var e *panicError
		if errors.As(c.err, &e) {
			// In order to prevent the waiting channels from being blocked forever,
			// needs to ensure that this panic cannot be recovered.
			panic(e)
		}
		// Normal return
	}()

	func() {
		defer func() {
			if !normalReturn {
				// Ideally, we would wait to take a stack trace until we've determined
				// whether this is a panic or a runtime.Goexit.
				//
				// Unfortunately, the only way we can distinguish the two is to see
				// whether the recover stopped the goroutine from terminating, and by
				// the time we know that, the part of the stack trace relevant to the
				// panic has been discarded.
				if r := recover(); r != nil {
					c.err = newPanicError(r)
				}
			}
		}()

		c.value, c.err = loader.Load(c.ctx, c.key)
		normalReturn = true
	}()

	if !normalReturn {
		recovered = true
	}
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
			c.wg.Done()
			return nil
		}
		return prevCall
	})
	return cl == nil
}

func (g *group[K, V]) delete(key K) {
	if !g.isInitialized.Load() {
		return
	}

	var prev *call[K, V]
	g.calls.Compute(key, func(prevCall *call[K, V]) *call[K, V] {
		prev = prevCall
		return nil
	})
	if prev != nil {
		prev.cancel()
	}
}
