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
	"slices"
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

// spec narrows in down to the configurations in which the key can be in state.
func (s prevState) spec(t *testing.T, in cacheSpec) cacheSpec {
	t.Helper()

	if s == prevExpired {
		// keep the case's own restriction, if any, but only its expiring kinds
		expiries := withExpiry
		if len(in.expiry) > 0 {
			expiries = slices.DeleteFunc(slices.Clone(in.expiry), func(e expiryKind) bool { return e == noExpiry })
		}
		// an empty dimension would mean all of them
		require.NotEmpty(t, expiries, "the spec has no configuration that expires entries")
		in.expiry = expiries
	}
	return in
}

// populate puts the key 1 in the state, with the value 1.
func (s prevState) populate(cc *cacheContext) {
	if s == prevAbsent {
		return
	}
	cc.cache.Set(1, 1)
	cc.run()
	if s == prevExpired {
		cc.advance(2 * specExpiry)
	}
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
	// spec is the configurations the mutation can run in.
	spec cacheSpec
	// run performs the mutation and returns what the call returned, in the int domain.
	run  func(t *testing.T, cc *cacheContext) any
	want map[prevState]mutationOutcome
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
	refreshes := cacheSpec{refresh: withRefresh}
	noLoad := func(ctx context.Context, key int) (int, error) {
		panic("the loader must not be called")
	}
	return []mutationCase{
		{
			name: "Set",
			run: func(t *testing.T, cc *cacheContext) any {
				cc.cache.Set(1, 2)
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
			run: func(t *testing.T, cc *cacheContext) any {
				v, ok := cc.cache.SetIfAbsent(1, 2)
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
			run: func(t *testing.T, cc *cacheContext) any {
				v, ok := cc.cache.Compute(1, func(int, bool) (int, ComputeOp) { return 2, WriteOp })
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
			run: func(t *testing.T, cc *cacheContext) any {
				v, ok := cc.cache.Compute(1, func(int, bool) (int, ComputeOp) { return 0, InvalidateOp })
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
			run: func(t *testing.T, cc *cacheContext) any {
				v, ok := cc.cache.Compute(1, func(int, bool) (int, ComputeOp) { return 0, CancelOp })
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
			run: func(t *testing.T, cc *cacheContext) any {
				v, ok := cc.cache.Invalidate(1)
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
			run: func(t *testing.T, cc *cacheContext) any {
				v, ok := cc.cache.ComputeIfAbsent(1, func() (int, bool) { return 2, false })
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
			run: func(t *testing.T, cc *cacheContext) any {
				v, ok := cc.cache.ComputeIfPresent(1, func(int) (int, ComputeOp) { return 2, WriteOp })
				return pair{v, ok}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: pair{0, false}},
				prevLive:    {result: pair{2, true}, value: 2, causes: []DeletionCause{repl}},
				prevExpired: {result: pair{0, false}, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "Get/load",
			run: func(t *testing.T, cc *cacheContext) any {
				v, err := cc.cache.Get(1, LoaderFunc[int, int](func(context.Context, int) (int, error) { return 2, nil }))
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
			run: func(t *testing.T, cc *cacheContext) any {
				v, err := cc.cache.Get(1, LoaderFunc[int, int](func(context.Context, int) (int, error) { return 0, ErrNotFound }))
				return loadResult{v, err}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: loadResult{0, ErrNotFound}},
				prevLive:    {result: loadResult{1, nil}, value: 1},
				prevExpired: {result: loadResult{0, ErrNotFound}, causes: []DeletionCause{exp}},
			},
		},
		{
			// A write during the load cancels it: the loaded value is discarded, and Get returns
			// the written one.
			name: "Get/stale",
			run: func(t *testing.T, cc *cacheContext) any {
				v, err := cc.cache.Get(1, LoaderFunc[int, int](func(context.Context, int) (int, error) {
					cc.cache.Set(1, 3)
					return 2, nil
				}))
				return loadResult{v, err}
			},
			want: map[prevState]mutationOutcome{
				prevAbsent:  {result: loadResult{3, nil}, value: 3},
				prevLive:    {result: loadResult{1, nil}, value: 1},
				prevExpired: {result: loadResult{3, nil}, value: 3, replacedBy: 3, causes: []DeletionCause{exp}},
			},
		},
		{
			name: "Refresh",
			spec: refreshes,
			run: func(t *testing.T, cc *cacheContext) any {
				r := refreshNow(cc, LoaderFunc[int, int](func(context.Context, int) (int, error) { return 2, nil }))
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
			spec: refreshes,
			run: func(t *testing.T, cc *cacheContext) any {
				r := refreshNow(cc, LoaderFunc[int, int](func(context.Context, int) (int, error) { return 0, ErrNotFound }))
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
			spec: cacheSpec{refresh: withRefresh, logs: true},
			run: func(t *testing.T, cc *cacheContext) any {
				r := refreshNow(cc, LoaderFunc[int, int](func(context.Context, int) (int, error) { return 0, errMutationLoad }))
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
			spec: refreshes,
			run: func(t *testing.T, cc *cacheContext) any {
				r := refreshNow(cc, LoaderFunc[int, int](func(context.Context, int) (int, error) {
					cc.cache.Set(1, 3)
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
			run: func(t *testing.T, cc *cacheContext) any {
				v, err := cc.cache.Get(1, LoaderFunc[int, int](noLoad))
				return loadResult{v, err}
			},
			want: map[prevState]mutationOutcome{
				prevLive: {result: loadResult{1, nil}, value: 1},
			},
		},
	}
}

// Every key mutation (Set, SetIfAbsent, the Compute family, Invalidate, and the completion of a
// load or refresh) over an absent, live or expired key, in every configuration of the cache the
// mutation and the state of the key can occur in.
func TestCache_KeyMutations(t *testing.T) {
	t.Parallel()

	for _, tc := range mutationCases() {
		for _, state := range []prevState{prevAbsent, prevLive, prevExpired} {
			want, ok := tc.want[state]
			if !ok {
				continue
			}
			t.Run(tc.name+"/"+state.String(), func(t *testing.T) {
				t.Parallel()

				forEachSpec(t, state.spec(t, tc.spec), func(t *testing.T, cc *cacheContext) {
					state.populate(cc)
					before := cc.cache.nodeID(1)

					got := tc.run(t, cc)
					cc.run()
					require.Equal(t, want.result, got, "result")
					cc.requireValue(t, 1, want.value)
					if state == prevLive && want.value == 2 && want.replacedBy == 0 {
						// the previous node was written over in place, if the configuration allows
						require.Equal(t, cc.updatesInPlace(), cc.cache.nodeID(1) == before, "updated in place")
					}
					if state == prevExpired && want.value == 2 && want.replacedBy == 0 {
						// an expired node is replaced (see atomicSet)
						require.False(t, cc.cache.nodeID(1) == before, "an expired node was reused")
					}

					var events []DeletionEvent[int, int]
					for _, cause := range want.causes {
						events = append(events, DeletionEvent[int, int]{Key: 1, Value: 1, Cause: cause})
					}
					cc.requireDeletions(t, events...)
				})
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
		op    func(cc *cacheContext)
		// want is the key's value afterwards: 2 if the refresh was applied, 0 if absent
		want int
	}{
		{name: "Set", state: prevLive, op: func(cc *cacheContext) { cc.cache.Set(1, 3) }, want: 3},
		{name: "Compute/WriteOp", state: prevLive, op: func(cc *cacheContext) {
			cc.cache.Compute(1, func(int, bool) (int, ComputeOp) { return 3, WriteOp })
		}, want: 3},
		{name: "Compute/InvalidateOp", state: prevLive, op: func(cc *cacheContext) {
			cc.cache.Compute(1, func(int, bool) (int, ComputeOp) { return 0, InvalidateOp })
		}},
		{name: "Invalidate", state: prevLive, op: func(cc *cacheContext) { cc.cache.Invalidate(1) }},
		{name: "Compute/CancelOp", state: prevLive, op: func(cc *cacheContext) {
			cc.cache.Compute(1, func(int, bool) (int, ComputeOp) { return 0, CancelOp })
		}, want: 2},
		{name: "SetIfAbsent", state: prevLive, op: func(cc *cacheContext) { cc.cache.SetIfAbsent(1, 3) }, want: 2},
		// The removal of an expired entry is not a change of the key: the refresh of the absent
		// key loads it, as a Get would.
		{name: "Compute/CancelOp/expired", state: prevExpired, op: func(cc *cacheContext) {
			cc.cache.Compute(1, func(int, bool) (int, ComputeOp) { return 0, CancelOp })
		}, want: 2},
		{name: "expiration", state: prevExpired, op: func(cc *cacheContext) { cc.cache.CleanUp() }, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := tt.state.spec(t, cacheSpec{refresh: withRefresh})
			forEachSpec(t, spec, func(t *testing.T, cc *cacheContext) {
				tt.state.populate(cc)

				res := cc.cache.Refresh(1, LoaderFunc[int, int](func(context.Context, int) (int, error) { return 2, nil }))
				tt.op(cc)
				cc.run()
				<-res
				cc.requireValue(t, 1, tt.want)
			})
		})
	}
}

// A mutation decides whether the key's entry is live with the time it reads under the bucket
// lock. An entry can expire while the mutation waits for the lock, here held by a Compute of the
// key, and a read in between finds the key absent: the mutation must find it absent too. With
// the time read before the lock, SetIfAbsent kept the expired value and reported it, Invalidate
// reported it present, Compute was given it, and SetExpiresAfter and a refresh of it brought the
// key back.
func TestCache_MutationDecidesWithTimeTakenUnderLock(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		// absent runs the mutation of key 1 and reports whether it found the key absent
		absent func(c *Cache[int, int]) bool
	}{
		{name: "SetIfAbsent", absent: func(c *Cache[int, int]) bool {
			v, ok := c.SetIfAbsent(1, 2)
			return ok && v == 2
		}},
		{name: "Invalidate", absent: func(c *Cache[int, int]) bool {
			_, ok := c.Invalidate(1)
			return !ok
		}},
		{name: "Compute", absent: func(c *Cache[int, int]) bool {
			var found bool
			c.Compute(1, func(_ int, ok bool) (int, ComputeOp) {
				found = ok
				return 0, CancelOp
			})
			return !found
		}},
		{name: "SetExpiresAfter", absent: func(c *Cache[int, int]) bool {
			c.SetExpiresAfter(1, time.Hour)
			_, ok := c.GetIfPresent(1)
			return !ok
		}},
		{name: "refresh", absent: func(c *Cache[int, int]) bool {
			// a refresh of an absent key loads it, one of a live key reloads it
			<-c.Refresh(ctx, 1, reloadAdds{})
			v, ok := c.GetIfPresent(1)
			return ok && v == 10
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := newNonTickingClock()
			c := Must(&Options[int, int]{
				Clock:             clk,
				ExpiryCalculator:  ExpiryWriting[int, int](time.Minute),
				RefreshCalculator: RefreshWriting[int, int](time.Hour),
				// goroutines of this test, which waitForFrame tells apart
				Executor: func(fn func()) { go fn() },
			})
			c.Set(1, 1) // expires at 60s
			clk.Sleep(50 * time.Second)

			entered, proceed := make(chan struct{}), make(chan struct{})
			held := make(chan struct{})
			go func() {
				defer close(held)
				c.Compute(1, func(old int, _ bool) (int, ComputeOp) {
					close(entered)
					<-proceed
					return old, CancelOp
				})
			}()
			<-entered
			absent := make(chan bool, 1)
			go func() {
				absent <- tt.absent(c)
			}()
			waitForFrame(t, bucketLockFrame, "the mutation did not wait for the bucket lock")
			// The entry expires. Nothing reads it here: a read would schedule maintenance, which
			// could remove the entry before the mutation takes the lock. A value written with the
			// time read at 50s, before the lock, would live until 110s.
			clk.Sleep(20 * time.Second)
			close(proceed)
			<-held
			require.True(t, <-absent, "the mutation found the entry that expired while it waited for the lock")
		})
	}
}

// The OnDeletion listener runs on the executor, so its events of one key arrive in the order of
// the mutations only if every mutation submits its event when it is applied. A mutation that
// replaced the node, or removed it, submitted its event only when maintenance replayed its task,
// while an update in place submitted it at once: with an executor that runs tasks in order, the
// event of an in-place update overtook that of the replacement or removal before it.
func TestCache_DeletionEventsFollowMutations(t *testing.T) {
	t.Parallel()

	forEachSpec(t, cacheSpec{}, func(t *testing.T, cc *cacheContext) {
		cc.cache.Set(1, 1)
		cc.cache.Set(1, 2) // replaces the node of a boxed value
		cc.cache.Set(1, 3) // in place
		cc.cache.Invalidate(1)
		cc.cache.Set(1, 4)
		cc.cache.Compute(1, func(int, bool) (int, ComputeOp) { return 5, WriteOp }) // in place
		cc.cache.Compute(1, func(int, bool) (int, ComputeOp) { return 0, InvalidateOp })
		cc.run()

		cc.requireDeletions(t,
			DeletionEvent[int, int]{Key: 1, Value: 1, Cause: CauseReplacement},
			DeletionEvent[int, int]{Key: 1, Value: 2, Cause: CauseReplacement},
			DeletionEvent[int, int]{Key: 1, Value: 3, Cause: CauseInvalidation},
			DeletionEvent[int, int]{Key: 1, Value: 4, Cause: CauseReplacement},
			DeletionEvent[int, int]{Key: 1, Value: 5, Cause: CauseInvalidation},
		)
	})
}

// The previous value is reported before the write reaches the policies, so a listener that
// writes the key it is told about, on an executor that runs it on the caller, hands its own write
// to the policies first. Here the listener adds the key back and invalidates it again, so the task
// that adds each node is handed off before the task that removes the node before it. The policies
// replay tasks in any order, and the cache stays consistent.
func TestCache_DeletionListenerWritesTheKey(t *testing.T) {
	t.Parallel()

	const nested = 50
	forEachSpec(t, cacheSpec{executor: []executorKind{callerRuns}}, func(t *testing.T, cc *cacheContext) {
		writes := 0
		cc.onDeletion = func(e DeletionEvent[int, int]) {
			if e.Cause == CauseInvalidation && writes < nested {
				writes++
				cc.cache.Set(e.Key, 2+writes)
				if writes < nested {
					cc.cache.Invalidate(e.Key)
				}
			}
		}
		cc.cache.Set(1, 1)
		cc.cache.Invalidate(1)
		require.Equal(t, nested, writes)
		cc.requireValue(t, 1, 2+nested)
	})
}
