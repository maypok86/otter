// Copyright (c) 2025 Alexey Mayshev and contributors. All rights reserved.
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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// prevState is what the key holds before a mutation.
type prevState int

const (
	prevAbsent prevState = iota
	prevLive
	// prevExpired is an expired entry that is still in the table.
	prevExpired
)

func (s prevState) String() string {
	return [...]string{"absent", "live", "expired"}[s]
}

// mutationOutcome is what a mutation must leave behind, in values of the int domain: 1 is the
// value the key held before, 2 the one the mutation writes, 3 the one a concurrent write stores.
type mutationOutcome struct {
	// result is what the call returned, from the case's result function.
	result any
	// value is the key's value afterwards; 0 means absent.
	value int
	// causes are the causes of the deletion events of the previous value (1), in order.
	causes []DeletionCause
	// replacedBy is the value whose concurrent write deleted the previous value instead, if any.
	replacedBy int
}

type mutationCase struct {
	name string
	// run performs the mutation and returns what the call returned, in the int domain.
	run  func(t *testing.T, m *mutationHarness) any
	want map[prevState]mutationOutcome
}

// mutationHarness is a cache whose executor queues tasks until run is called, so that an
// expired entry stays in the table, and whose deletion listeners are recorded.
type mutationHarness struct {
	clk      *nonTickingClock
	run      func()
	set      func(key, value int)
	get      func(key int) (int, bool)
	compute  func(key int, fn func(oldValue int, found bool) (int, ComputeOp)) (int, bool)
	loader   func(load func(ctx context.Context, key int) (int, error)) Loader[int, int]
	refresh  func(key int, loader Loader[int, int]) <-chan RefreshResult[int, int]
	getLoad  func(key int, loader Loader[int, int]) (int, error)
	setIfAbs func(key, value int) (int, bool)
	inval    func(key int) (int, bool)
	cia      func(key int, fn func() (int, bool)) (int, bool)
	cip      func(key int, fn func(oldValue int) (int, ComputeOp)) (int, bool)
	nodeID   func(key int) any
	// startRefresh requests a refresh that loads value and leaves it queued on the executor.
	startRefresh func(key, value int)
	validate     func(t *testing.T)

	mu            sync.Mutex
	atomicEvents  []DeletionEvent[int, int]
	deleteEvents  []DeletionEvent[int, int]
	pendingLength func() int
}

// newMutationHarness builds a harness over a cache of V, which tells the int domain apart from
// V with toV and fromV.
func newMutationHarness[V comparable](toV func(int) V, fromV func(V) int) *mutationHarness {
	m := &mutationHarness{clk: newNonTickingClock()}
	var pending []func()
	m.run = func() {
		for len(pending) > 0 {
			fn := pending[0]
			pending = pending[1:]
			fn()
		}
	}
	m.pendingLength = func() int { return len(pending) }
	record := func(events *[]DeletionEvent[int, int]) func(e DeletionEvent[int, V]) {
		return func(e DeletionEvent[int, V]) {
			m.mu.Lock()
			defer m.mu.Unlock()
			*events = append(*events, DeletionEvent[int, int]{Key: e.Key, Value: fromV(e.Value), Cause: e.Cause})
		}
	}
	c := Must(&Options[int, V]{
		MaximumSize:       10,
		Clock:             m.clk,
		ExpiryCalculator:  ExpiryWriting[int, V](time.Minute),
		RefreshCalculator: RefreshWriting[int, V](time.Hour),
		OnAtomicDeletion:  record(&m.atomicEvents),
		OnDeletion:        record(&m.deleteEvents),
		Logger:            &NoopLogger{},
		Executor: func(fn func()) {
			pending = append(pending, fn)
		},
	})
	toLoader := func(l Loader[int, int]) Loader[int, V] {
		return LoaderFunc[int, V](func(ctx context.Context, key int) (V, error) {
			v, err := l.Load(ctx, key)
			return toV(v), err
		})
	}
	m.set = func(key, value int) { c.Set(key, toV(value)) }
	m.get = func(key int) (int, bool) {
		v, ok := c.GetIfPresent(key)
		return fromV(v), ok
	}
	m.compute = func(key int, fn func(int, bool) (int, ComputeOp)) (int, bool) {
		v, ok := c.Compute(key, func(oldValue V, found bool) (V, ComputeOp) {
			nv, op := fn(fromV(oldValue), found)
			return toV(nv), op
		})
		return fromV(v), ok
	}
	m.loader = func(load func(ctx context.Context, key int) (int, error)) Loader[int, int] {
		return LoaderFunc[int, int](load)
	}
	m.refresh = func(key int, l Loader[int, int]) <-chan RefreshResult[int, int] {
		ch := make(chan RefreshResult[int, int], 1)
		res := c.Refresh(context.Background(), key, toLoader(l))
		m.run()
		r := <-res
		ch <- RefreshResult[int, int]{Key: r.Key, Value: fromV(r.Value), Err: r.Err}
		return ch
	}
	m.getLoad = func(key int, l Loader[int, int]) (int, error) {
		v, err := c.Get(context.Background(), key, toLoader(l))
		return fromV(v), err
	}
	m.setIfAbs = func(key, value int) (int, bool) {
		v, ok := c.SetIfAbsent(key, toV(value))
		return fromV(v), ok
	}
	m.inval = func(key int) (int, bool) {
		v, ok := c.Invalidate(key)
		return fromV(v), ok
	}
	m.cia = func(key int, fn func() (int, bool)) (int, bool) {
		v, ok := c.ComputeIfAbsent(key, func() (V, bool) {
			nv, cancel := fn()
			return toV(nv), cancel
		})
		return fromV(v), ok
	}
	m.cip = func(key int, fn func(int) (int, ComputeOp)) (int, bool) {
		v, ok := c.ComputeIfPresent(key, func(oldValue V) (V, ComputeOp) {
			nv, op := fn(fromV(oldValue))
			return toV(nv), op
		})
		return fromV(v), ok
	}
	m.startRefresh = func(key, value int) {
		c.Refresh(context.Background(), key, LoaderFunc[int, V](func(context.Context, int) (V, error) {
			return toV(value), nil
		}))
	}
	m.nodeID = func(key int) any {
		n := c.cache.hashmap.Get(key)
		if n == nil {
			return nil
		}
		return n.AsPointer()
	}
	m.validate = func(t *testing.T) {
		t.Helper()
		c.CleanUp()
		m.run()
		validateCache(t, c)
	}
	return m
}

type pair struct {
	v  int
	ok bool
}

type loadResult struct {
	v   int
	err error
}

var errMutationLoad = errors.New("load failed")

// mutationCases is the behavior every key mutation keeps: what the call returns, what the key
// holds afterwards, and which deletion events both listeners get, for each state of the key.
func mutationCases() []mutationCase {
	const (
		repl = CauseReplacement
		inv  = CauseInvalidation
		exp  = CauseExpiration
	)
	noLoad := func(ctx context.Context, key int) (int, error) {
		panic("the loader must not be called")
	}
	return []mutationCase{
		{
			name: "Set",
			run: func(t *testing.T, m *mutationHarness) any {
				m.set(1, 2)
				return nil
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {value: 2},
				prevLive:    {value: 2, causes: []DeletionCause{repl}},
				prevExpired: {value: 2, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "SetIfAbsent",
			run: func(t *testing.T, m *mutationHarness) any {
				v, ok := m.setIfAbs(1, 2)
				return pair{v, ok}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: pair{2, true}, value: 2},
				prevLive:    {result: pair{1, false}, value: 1},
				prevExpired: {result: pair{2, true}, value: 2, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "Compute/WriteOp",
			run: func(t *testing.T, m *mutationHarness) any {
				v, ok := m.compute(1, func(int, bool) (int, ComputeOp) { return 2, WriteOp })
				return pair{v, ok}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: pair{2, true}, value: 2},
				prevLive:    {result: pair{2, true}, value: 2, causes: []DeletionCause{repl}},
				prevExpired: {result: pair{2, true}, value: 2, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "Compute/InvalidateOp",
			run: func(t *testing.T, m *mutationHarness) any {
				v, ok := m.compute(1, func(int, bool) (int, ComputeOp) { return 0, InvalidateOp })
				return pair{v, ok}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: pair{0, false}},
				prevLive:    {result: pair{0, false}, causes: []DeletionCause{inv}},
				prevExpired: {result: pair{0, false}, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "Compute/CancelOp",
			run: func(t *testing.T, m *mutationHarness) any {
				v, ok := m.compute(1, func(int, bool) (int, ComputeOp) { return 0, CancelOp })
				return pair{v, ok}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: pair{0, false}},
				prevLive:    {result: pair{1, true}, value: 1},
				prevExpired: {result: pair{0, false}, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "Invalidate",
			run: func(t *testing.T, m *mutationHarness) any {
				v, ok := m.inval(1)
				return pair{v, ok}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: pair{0, false}},
				prevLive:    {result: pair{1, true}, causes: []DeletionCause{inv}},
				prevExpired: {result: pair{0, false}, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "ComputeIfAbsent",
			run: func(t *testing.T, m *mutationHarness) any {
				v, ok := m.cia(1, func() (int, bool) { return 2, false })
				return pair{v, ok}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: pair{2, true}, value: 2},
				prevLive:    {result: pair{1, true}, value: 1},
				prevExpired: {result: pair{2, true}, value: 2, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "ComputeIfPresent",
			run: func(t *testing.T, m *mutationHarness) any {
				v, ok := m.cip(1, func(int) (int, ComputeOp) { return 2, WriteOp })
				return pair{v, ok}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent: {result: pair{0, false}},
				prevLive:   {result: pair{2, true}, value: 2, causes: []DeletionCause{repl}},
				// the miss schedules maintenance, and the timer wheel removes the expired entry
				prevExpired: {result: pair{0, false}, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "Get/load",
			run: func(t *testing.T, m *mutationHarness) any {
				v, err := m.getLoad(1, m.loader(func(context.Context, int) (int, error) { return 2, nil }))
				return loadResult{v, err}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: loadResult{2, nil}, value: 2},
				prevLive:    {result: loadResult{1, nil}, value: 1},
				prevExpired: {result: loadResult{2, nil}, value: 2, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "Get/not found",
			run: func(t *testing.T, m *mutationHarness) any {
				v, err := m.getLoad(1, m.loader(func(context.Context, int) (int, error) { return 0, ErrNotFound }))
				return loadResult{v, err}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: loadResult{0, ErrNotFound}},
				prevLive:    {result: loadResult{1, nil}, value: 1},
				prevExpired: {result: loadResult{0, ErrNotFound}, causes: []DeletionCause{exp}},
			},
		},
		{
			// A write during the load cancels it, and the loaded value is discarded.
			name: "Get/stale",
			run: func(t *testing.T, m *mutationHarness) any {
				v, err := m.getLoad(1, m.loader(func(context.Context, int) (int, error) {
					m.set(1, 3)
					return 2, nil
				}))
				return loadResult{v, err}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: loadResult{2, nil}, value: 3},
				prevLive:    {result: loadResult{1, nil}, value: 1},
				prevExpired: {result: loadResult{2, nil}, value: 3, replacedBy: 3, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "Refresh",
			run: func(t *testing.T, m *mutationHarness) any {
				r := <-m.refresh(1, m.loader(func(context.Context, int) (int, error) { return 2, nil }))
				return loadResult{r.Value, r.Err}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: loadResult{2, nil}, value: 2},
				prevLive:    {result: loadResult{2, nil}, value: 2, causes: []DeletionCause{repl}},
				prevExpired: {result: loadResult{2, nil}, value: 2, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "Refresh/not found",
			run: func(t *testing.T, m *mutationHarness) any {
				r := <-m.refresh(1, m.loader(func(context.Context, int) (int, error) { return 0, ErrNotFound }))
				return loadResult{r.Value, r.Err}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: loadResult{0, ErrNotFound}},
				prevLive:    {result: loadResult{0, ErrNotFound}, causes: []DeletionCause{inv}},
				prevExpired: {result: loadResult{0, ErrNotFound}, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "Refresh/error",
			run: func(t *testing.T, m *mutationHarness) any {
				r := <-m.refresh(1, m.loader(func(context.Context, int) (int, error) { return 0, errMutationLoad }))
				return loadResult{r.Value, r.Err}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: loadResult{0, errMutationLoad}},
				prevLive:    {result: loadResult{0, errMutationLoad}, value: 1},
				prevExpired: {result: loadResult{0, errMutationLoad}},
			},
		},
		{
			// A write during the refresh cancels it, and the loaded value is discarded.
			name: "Refresh/stale",
			run: func(t *testing.T, m *mutationHarness) any {
				r := <-m.refresh(1, m.loader(func(context.Context, int) (int, error) {
					m.set(1, 3)
					return 2, nil
				}))
				return loadResult{r.Value, r.Err}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: loadResult{2, nil}, value: 3},
				prevLive:    {result: loadResult{2, nil}, value: 3, replacedBy: 3, causes: []DeletionCause{repl}},
				prevExpired: {result: loadResult{2, nil}, value: 3, replacedBy: 3, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "Get/hit",
			run: func(t *testing.T, m *mutationHarness) any {
				v, err := m.getLoad(1, m.loader(noLoad))
				return loadResult{v, err}
			},
			want: map[prevState]mutationOutcome{
				prevLive: {result: loadResult{1, nil}, value: 1},
			},
		},
	}
}

// Every key mutation (Set, SetIfAbsent, the Compute family, Invalidate, and the completion of a
// load or refresh) over an absent, live or expired key, for values updated in place (int) and
// values whose node is replaced on the first update ([2]int).
func TestCache_KeyMutations(t *testing.T) {
	t.Parallel()

	t.Run("in place", func(t *testing.T) {
		t.Parallel()
		testKeyMutations(t, true, func(v int) int { return v }, func(v int) int { return v })
	})
	t.Run("replacement", func(t *testing.T) {
		t.Parallel()
		testKeyMutations(t, false, func(v int) [2]int { return [2]int{v, v} }, func(v [2]int) int { return v[0] })
	})
}

func testKeyMutations[V comparable](t *testing.T, inPlace bool, toV func(int) V, fromV func(V) int) {
	t.Helper()

	for _, tc := range mutationCases() {
		for _, state := range []prevState{prevAbsent, prevLive, prevExpired} {
			want, ok := tc.want[state]
			if !ok {
				continue
			}
			t.Run(tc.name+"/"+state.String(), func(t *testing.T) {
				t.Parallel()

				m := newMutationHarness(toV, fromV)
				if state != prevAbsent {
					m.set(1, 1)
					m.run()
				}
				if state == prevExpired {
					m.clk.Sleep(2 * time.Minute)
				}
				before := m.nodeID(1)

				got := tc.run(t, m)
				m.run()
				require.Equal(t, want.result, got, "result")
				v, found := m.get(1)
				if want.value == 0 {
					require.False(t, found, "the key holds %d", v)
				} else {
					require.True(t, found)
					require.Equal(t, want.value, v)
				}
				if state != prevAbsent && want.value == 2 && want.replacedBy == 0 {
					// the previous node was written over: in place for words, replaced for the rest
					require.Equal(t, inPlace, m.nodeID(1) == before, "updated in place")
				}

				var events []DeletionEvent[int, int]
				for _, cause := range want.causes {
					events = append(events, DeletionEvent[int, int]{Key: 1, Value: 1, Cause: cause})
				}
				m.mu.Lock()
				atomicEvents, deleteEvents := m.atomicEvents, m.deleteEvents
				m.mu.Unlock()
				require.Equal(t, events, atomicEvents, "OnAtomicDeletion")
				require.Equal(t, events, deleteEvents, "OnDeletion")

				m.validate(t)
				require.Zero(t, m.pendingLength())
			})
		}
	}
}

// A write or an invalidation cancels a refresh requested before it, so that the refresh cannot
// bring back the value it replaced. Keeping the entry does not, and removing an expired entry
// drops only a refresh of it, as the timer wheel does.
func TestCache_KeyMutationsCancelPendingRefresh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state prevState
		op    func(m *mutationHarness)
		// want is the key's value afterwards: 2 if the refresh was applied, 0 if absent
		want int
	}{
		{name: "Set", state: prevLive, op: func(m *mutationHarness) { m.set(1, 3) }, want: 3},
		{name: "Compute/WriteOp", state: prevLive, op: func(m *mutationHarness) {
			m.compute(1, func(int, bool) (int, ComputeOp) { return 3, WriteOp })
		}, want: 3},
		{name: "Compute/InvalidateOp", state: prevLive, op: func(m *mutationHarness) {
			m.compute(1, func(int, bool) (int, ComputeOp) { return 0, InvalidateOp })
		}},
		{name: "Invalidate", state: prevLive, op: func(m *mutationHarness) { m.inval(1) }},
		{name: "Compute/CancelOp", state: prevLive, op: func(m *mutationHarness) {
			m.compute(1, func(int, bool) (int, ComputeOp) { return 0, CancelOp })
		}, want: 2},
		{name: "SetIfAbsent", state: prevLive, op: func(m *mutationHarness) { m.setIfAbs(1, 3) }, want: 2},
		{name: "Compute/CancelOp/expired", state: prevExpired, op: func(m *mutationHarness) {
			m.compute(1, func(int, bool) (int, ComputeOp) { return 0, CancelOp })
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMutationHarness(func(v int) int { return v }, func(v int) int { return v })
			m.set(1, 1)
			m.run()
			if tt.state == prevExpired {
				m.clk.Sleep(2 * time.Minute)
			}

			m.startRefresh(1, 2)
			tt.op(m)
			m.run()
			v, ok := m.get(1)
			if tt.want == 0 {
				require.False(t, ok, "the key holds %d", v)
			} else {
				require.True(t, ok)
				require.Equal(t, tt.want, v)
			}
			m.validate(t)
		})
	}
}
