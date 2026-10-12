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

// Package lincheck checks that histories of concurrent cache operations are linearizable: that
// some order of the operations, each taking effect at one point between its call and its
// return, explains every result. It records histories of random operations on a few keys and
// checks them with porcupine against a sequential model of the cache, key by key.
//
// The cache's clock never moves, so no entry expires, and every expiration and refresh time is
// the clock's time plus the duration a calculator or a setter gave it. Some histories run on a
// cache that holds one entry and evicts all the time: each eviction is recorded as an operation
// of its own, which starts when OnAtomicDeletion reports it, before the entry is removed, and
// ends when OnDeletion does, after. So the model is exact. Values written by the test are positive, values the loader
// returns are negative, and both are unique, so the model tells a loaded value from a written one.
package lincheck

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anishathalye/porcupine"

	"github.com/maypok86/otter/v2"
)

type opKind uint8

const (
	opGet opKind = iota
	opBulkGet
	opGetIfPresent
	opSet
	opSetIfAbsent
	opInvalidate
	// opComputeIfAbsent writes value if the key is absent and keeps the key otherwise.
	opComputeIfAbsent
	// opComputeIfPresent writes value if the key is present and keeps it absent otherwise.
	opComputeIfPresent
	// opComputeDelete invalidates the key if it is present.
	opComputeDelete
	opRefresh
	// opSetExpiresAfter and opSetRefreshableAfter set the time to value hours from now.
	opSetExpiresAfter
	opSetRefreshableAfter
	// opExpiresAt and opRefreshableAt read the time from GetEntryQuietly, in hours from now. An
	// entry's fields are read one at a time and may come from different writes, so each op
	// reads one of them.
	opExpiresAt
	opRefreshableAt
	// opCount is the number of operations that clients run.
	opCount
	// opEvict is the eviction of value by the cache's size bound; the cache runs it on its own.
	opEvict = opCount
)

var opNames = [...]string{
	opGet:                 "Get",
	opBulkGet:             "BulkGet",
	opGetIfPresent:        "GetIfPresent",
	opSet:                 "Set",
	opSetIfAbsent:         "SetIfAbsent",
	opInvalidate:          "Invalidate",
	opComputeIfAbsent:     "ComputeIfAbsent",
	opComputeIfPresent:    "ComputeIfPresent",
	opComputeDelete:       "ComputeDelete",
	opRefresh:             "Refresh",
	opSetExpiresAfter:     "SetExpiresAfter",
	opSetRefreshableAfter: "SetRefreshableAfter",
	opExpiresAt:           "ExpiresAt",
	opRefreshableAt:       "RefreshableAt",
	opEvict:               "Evict",
}

// result is how a load ended, as its caller saw it.
type result uint8

const (
	loaded result = iota
	notFound
	failed
)

type input struct {
	op    opKind
	key   int
	value int // the value written, for writes; hours, for the setters
}

type output struct {
	value  int
	ok     bool   // the second result of the method, where it has one
	result result // for loads and refreshes
}

// The calculators keep both times on update and reload (ExpiryCreating, RefreshCreating), so
// only a creation and the setters change them.
const (
	expiresAfter     = 100 // hours
	refreshableAfter = 200 // hours
)

// state is one key: its value, 0 if it is absent, and its expiration and refresh times in hours
// from now, 0 if there are none.
type state struct {
	value         int
	expiresIn     int
	refreshableIn int
}

func isLoaded(v int) bool { return v < 0 }

// newModel returns the model of a cache with or without expiration; refresh is always on.
func newModel(expiry bool) porcupine.NondeterministicModel {
	created := func(v int) state {
		s := state{value: v, refreshableIn: refreshableAfter}
		if expiry {
			s.expiresIn = expiresAfter
		}
		return s
	}
	updated := func(s state, v int) state {
		s.value = v
		return s
	}
	written := func(s state, v int) state {
		if s.value == 0 {
			return created(v)
		}
		return updated(s, v)
	}

	// step returns the states that op can lead to from s, if it can return out; none if it cannot.
	step := func(st, in, out any) []any {
		s, i, o := st.(state), in.(input), out.(output)
		present := s.value != 0
		switch i.op {
		case opGet, opBulkGet:
			if present {
				// a hit: neither the loader nor its errors are involved
				return when(o.result == loaded && o.value == s.value, s)
			}
			switch o.result {
			case loaded:
				return when(isLoaded(o.value), created(o.value))
			case notFound, failed:
				return []any{s}
			}
		case opGetIfPresent:
			if present {
				return when(o.ok && o.value == s.value, s)
			}
			return when(!o.ok && o.value == 0, s)
		case opSet:
			return []any{written(s, i.value)}
		case opSetIfAbsent:
			if present {
				return when(!o.ok && o.value == s.value, s)
			}
			return when(o.ok && o.value == i.value, created(i.value))
		case opInvalidate:
			if present {
				return when(o.ok && o.value == s.value, state{})
			}
			return when(!o.ok && o.value == 0, s)
		case opComputeIfAbsent:
			if present {
				return when(o.ok && o.value == s.value, s)
			}
			return when(o.ok && o.value == i.value, created(i.value))
		case opComputeIfPresent:
			if present {
				return when(o.ok && o.value == i.value, updated(s, i.value))
			}
			return when(!o.ok && o.value == 0, s)
		case opComputeDelete:
			return when(!o.ok && o.value == 0, state{})
		case opRefresh:
			// The refresh is discarded if the key was written or invalidated after it was requested,
			// which depends on the history, not on the state: either outcome is allowed.
			switch o.result {
			case loaded:
				return []any{s, written(s, o.value)}
			case notFound:
				return []any{s, state{}}
			case failed:
				return []any{s}
			}
		case opSetExpiresAfter:
			if present && expiry {
				s.expiresIn = i.value
			}
			return []any{s}
		case opSetRefreshableAfter:
			if present {
				s.refreshableIn = i.value
			}
			return []any{s}
		case opExpiresAt:
			if present {
				return when(o.ok && o.value == s.expiresIn, s)
			}
			return when(!o.ok, s)
		case opRefreshableAt:
			if present {
				return when(o.ok && o.value == s.refreshableIn, s)
			}
			return when(!o.ok, s)
		case opEvict:
			return when(s.value == i.value, state{})
		}
		return nil
	}

	return porcupine.NondeterministicModel{
		Partition: func(history []porcupine.Operation) [][]porcupine.Operation {
			byKey := make(map[int][]porcupine.Operation)
			for _, op := range history {
				k := op.Input.(input).key
				byKey[k] = append(byKey[k], op)
			}
			partitions := make([][]porcupine.Operation, 0, len(byKey))
			for _, ops := range byKey {
				partitions = append(partitions, ops)
			}
			return partitions
		},
		Init: func() []any { return []any{state{}} },
		Step: step,
		DescribeOperation: func(in, out any) string {
			i, o := in.(input), out.(output)
			switch i.op {
			case opSet:
				return fmt.Sprintf("Set(%d, %d)", i.key, i.value)
			case opSetExpiresAfter, opSetRefreshableAfter:
				return fmt.Sprintf("%s(%d, %dh)", opNames[i.op], i.key, i.value)
			case opEvict:
				return fmt.Sprintf("Evict(%d, %d)", i.key, i.value)
			case opSetIfAbsent, opComputeIfAbsent, opComputeIfPresent:
				return fmt.Sprintf("%s(%d, %d) -> %d, %t", opNames[i.op], i.key, i.value, o.value, o.ok)
			case opGet, opBulkGet, opRefresh:
				return fmt.Sprintf("%s(%d) -> %d, %s", opNames[i.op], i.key, o.value, [...]string{"loaded", "not found", "failed"}[o.result])
			case opExpiresAt, opRefreshableAt:
				return fmt.Sprintf("%s(%d) -> %dh, %t", opNames[i.op], i.key, o.value, o.ok)
			default:
				return fmt.Sprintf("%s(%d) -> %d, %t", opNames[i.op], i.key, o.value, o.ok)
			}
		},
		DescribeState: func(st any) string {
			s := st.(state)
			if s.value == 0 {
				return "absent"
			}
			in := func(hours int) string {
				if hours == 0 {
					return "never"
				}
				return fmt.Sprintf("in %dh", hours)
			}
			return fmt.Sprintf("%d (expires %s, refreshable %s)", s.value, in(s.expiresIn), in(s.refreshableIn))
		},
	}
}

func when(ok bool, s state) []any {
	if !ok {
		return nil
	}
	return []any{s}
}

// frozenClock is a clock that never moves: no entry expires or becomes stale.
type frozenClock struct{}

// now is the time of frozenClock; any positive value works.
const now = int64(time.Hour)

func (frozenClock) NowNano() int64                      { return now }
func (frozenClock) Tick(time.Duration) <-chan time.Time { return nil }

// hoursFromNow converts an expiration or refresh time to hours from now, 0 if there is none and
// -1 if it is not a whole number of hours, which no operation sets.
func hoursFromNow(nanos int64) int {
	if nanos > now+int64(1000*time.Hour) {
		return 0
	}
	d := nanos - now
	if d%int64(time.Hour) != 0 {
		return -1
	}
	return int(d / int64(time.Hour))
}

var errLoad = errors.New("load failed")

type discardLogger struct{}

func (discardLogger) Warn(context.Context, string, error)  {}
func (discardLogger) Error(context.Context, string, error) {}

// recorder runs random operations on a cache and records their history.
type recorder struct {
	cache   *otter.Cache[int, int]
	clock   atomic.Int64 // orders calls and returns
	written atomic.Int64 // the last positive value handed out
	loads   atomic.Int64 // the last negative value handed out, negated
	keys    int

	mu sync.Mutex
	// evicting holds the call time of each eviction whose OnDeletion has not run yet.
	evicting map[input]int64
	// evictions are the evictions that have ended.
	evictions []porcupine.Operation
	// sealed is set once the history is taken; later evictions are not observed by any operation.
	sealed bool
}

// config is the cache that a history runs on.
type config struct {
	name string
	// expiry gives entries an expiration time. Without it or a size bound, the cache replaces
	// the node on every write; with either, a live entry is updated in place.
	expiry bool
	// bounded limits the cache to one entry.
	bounded bool
}

var configs = [...]config{
	{name: "plain"},
	{name: "expiry", expiry: true},
	{name: "evicting", bounded: true},
	{name: "evicting_expiry", expiry: true, bounded: true},
}

func newRecorder(keys int, cfg config) *recorder {
	r := &recorder{keys: keys, evicting: make(map[input]int64)}
	o := &otter.Options[int, int]{
		Clock:             frozenClock{},
		RefreshCalculator: otter.RefreshCreating[int, int](refreshableAfter * time.Hour),
		// failed refreshes are expected
		Logger: discardLogger{},
	}
	if cfg.expiry {
		o.ExpiryCalculator = otter.ExpiryCreating[int, int](expiresAfter * time.Hour)
	}
	if cfg.bounded {
		o.MaximumSize = 1
		// Under the bucket lock, before the entry is removed.
		o.OnAtomicDeletion = func(e otter.DeletionEvent[int, int]) {
			if e.Cause == otter.CauseOverflow {
				r.startEviction(input{op: opEvict, key: e.Key, value: e.Value})
			}
		}
		// On the executor, after the entry is removed.
		o.OnDeletion = func(e otter.DeletionEvent[int, int]) {
			if e.Cause == otter.CauseOverflow {
				r.endEviction(input{op: opEvict, key: e.Key, value: e.Value})
			}
		}
	}
	r.cache = otter.Must(o)
	return r
}

func (r *recorder) startEviction(in input) {
	call := r.clock.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.sealed {
		r.evicting[in] = call
	}
}

func (r *recorder) endEviction(in input) {
	ret := r.clock.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if call, ok := r.evicting[in]; ok && !r.sealed {
		delete(r.evicting, in)
		r.evictions = append(r.evictions, porcupine.Operation{Input: in, Output: output{}, Call: call, Return: ret})
	}
}

// takeEvictions returns the evictions once the clients are done. It waits a little for the
// OnDeletion calls of the evictions in flight; one that has not come by then ends now, since the
// entry has already been removed or is removed later, after every operation of the clients.
func (r *recorder) takeEvictions() []porcupine.Operation {
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		r.mu.Lock()
		n := len(r.evicting)
		r.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sealed = true
	for in, call := range r.evicting {
		r.evictions = append(r.evictions, porcupine.Operation{Input: in, Output: output{}, Call: call, Return: r.clock.Add(1)})
	}
	return r.evictions
}

// load returns a fresh loaded value, or ends the load with not found or an error.
func (r *recorder) load(rnd *rand.Rand) (int, error) {
	switch n := rnd.IntN(10); {
	case n == 0:
		return 0, otter.ErrNotFound
	case n == 1:
		return 0, errLoad
	default:
		if rnd.IntN(4) == 0 {
			// a slow load leaves room for writes while it is in flight
			time.Sleep(time.Duration(rnd.IntN(50)) * time.Microsecond)
		}
		return int(-r.loads.Add(1)), nil
	}
}

func loadResult(err error) result {
	switch {
	case err == nil:
		return loaded
	case errors.Is(err, otter.ErrNotFound):
		return notFound
	default:
		return failed
	}
}

func (r *recorder) run(client int, ops int, seed uint64) []porcupine.Operation {
	ctx := context.Background()
	rnd := rand.New(rand.NewPCG(seed, uint64(client)))
	// The loaders run on other goroutines (refreshes run on the executor), so each draws from its
	// own source, guarded by a mutex.
	var mu sync.Mutex
	loaderRnd := rand.New(rand.NewPCG(seed, uint64(client)+1<<32))
	load := func() (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return r.load(loaderRnd)
	}
	loader := otter.LoaderFunc[int, int](func(context.Context, int) (int, error) { return load() })
	bulkLoader := otter.BulkLoaderFunc[int, int](func(_ context.Context, keys []int) (map[int]int, error) {
		res := make(map[int]int, len(keys))
		for _, k := range keys {
			v, err := load()
			switch {
			case errors.Is(err, otter.ErrNotFound):
			case err != nil:
				return nil, err
			default:
				res[k] = v
			}
		}
		return res, nil
	})

	history := make([]porcupine.Operation, 0, ops)
	for range ops {
		in := input{op: opKind(rnd.IntN(int(opCount))), key: rnd.IntN(r.keys)}
		switch in.op {
		case opSet, opSetIfAbsent, opComputeIfAbsent, opComputeIfPresent:
			in.value = int(r.written.Add(1))
		case opSetExpiresAfter, opSetRefreshableAfter:
			in.value = 1 + rnd.IntN(8)
		}
		var out output
		call := r.clock.Add(1)
		switch in.op {
		case opGet:
			v, err := r.cache.Get(ctx, in.key, loader)
			out = output{value: v, result: loadResult(err)}
		case opBulkGet:
			res, err := r.cache.BulkGet(ctx, []int{in.key}, bulkLoader)
			v, ok := res[in.key]
			switch {
			case err != nil:
				out = output{result: failed}
			case !ok:
				out = output{result: notFound}
			default:
				out = output{value: v, result: loaded}
			}
		case opGetIfPresent:
			out.value, out.ok = r.cache.GetIfPresent(in.key)
		case opSet:
			r.cache.Set(in.key, in.value)
		case opSetIfAbsent:
			out.value, out.ok = r.cache.SetIfAbsent(in.key, in.value)
		case opInvalidate:
			out.value, out.ok = r.cache.Invalidate(in.key)
		case opComputeIfAbsent:
			out.value, out.ok = r.cache.Compute(in.key, func(old int, found bool) (int, otter.ComputeOp) {
				if found {
					return old, otter.CancelOp
				}
				return in.value, otter.WriteOp
			})
		case opComputeIfPresent:
			out.value, out.ok = r.cache.Compute(in.key, func(old int, found bool) (int, otter.ComputeOp) {
				if !found {
					return old, otter.CancelOp
				}
				return in.value, otter.WriteOp
			})
		case opComputeDelete:
			out.value, out.ok = r.cache.Compute(in.key, func(old int, found bool) (int, otter.ComputeOp) {
				if !found {
					return old, otter.CancelOp
				}
				return old, otter.InvalidateOp
			})
		case opRefresh:
			rr := <-r.cache.Refresh(ctx, in.key, loader)
			out = output{value: rr.Value, result: loadResult(rr.Err)}
		case opSetExpiresAfter:
			r.cache.SetExpiresAfter(in.key, time.Duration(in.value)*time.Hour)
		case opSetRefreshableAfter:
			r.cache.SetRefreshableAfter(in.key, time.Duration(in.value)*time.Hour)
		case opExpiresAt:
			if e, ok := r.cache.GetEntryQuietly(in.key); ok {
				out = output{value: hoursFromNow(e.ExpiresAtNano), ok: true}
			}
		case opRefreshableAt:
			if e, ok := r.cache.GetEntryQuietly(in.key); ok {
				out = output{value: hoursFromNow(e.RefreshableAtNano), ok: true}
			}
		}
		history = append(history, porcupine.Operation{
			ClientId: client,
			Input:    in,
			Call:     call,
			Output:   out,
			Return:   r.clock.Add(1),
		})
	}
	return history
}

// record runs clients goroutines, each with ops operations, and returns their history.
func record(keys, clients, ops int, seed uint64, cfg config) []porcupine.Operation {
	r := newRecorder(keys, cfg)
	histories := make([][]porcupine.Operation, clients)
	var start, wg sync.WaitGroup
	start.Add(1)
	wg.Add(clients)
	for c := range clients {
		go func() {
			defer wg.Done()
			start.Wait()
			histories[c] = r.run(c, ops, seed)
		}()
	}
	start.Done()
	wg.Wait()

	// the evictions are the operations of one more client
	history := r.takeEvictions()
	for i := range history {
		history[i].ClientId = clients
	}
	for _, h := range histories {
		history = append(history, h...)
	}
	return history
}

// TestLinearizability records histories for each configuration, for an equal share of
// LINCHECK_DURATION (default 2s) each, and checks them. A history that is not linearizable is
// written as an HTML visualization to LINCHECK_OUT (default the test's temporary directory).
func TestLinearizability(t *testing.T) {
	duration := 2 * time.Second
	if d := os.Getenv("LINCHECK_DURATION"); d != "" {
		var err error
		if duration, err = time.ParseDuration(d); err != nil {
			t.Fatalf("LINCHECK_DURATION: %v", err)
		}
	}
	out := os.Getenv("LINCHECK_OUT")
	if out == "" {
		out = t.TempDir()
	}
	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			check(t, cfg, duration/time.Duration(len(configs)), out)
		})
	}
}

// check records histories on a cache with cfg until duration has passed and checks each of them.
func check(t *testing.T, cfg config, duration time.Duration, out string) {
	t.Helper()

	const (
		keys    = 2
		clients = 8
		ops     = 200
	)
	nm := newModel(cfg.expiry)
	m := nm.ToModel()
	seed := uint64(time.Now().UnixNano())
	deadline := time.Now().Add(duration)
	histories, unknown := 0, 0
	for ; time.Now().Before(deadline); seed++ {
		history := record(keys, clients, ops, seed, cfg)
		histories++
		res, info := porcupine.CheckOperationsVerbose(m, history, 10*time.Second)
		switch res {
		case porcupine.Ok:
		case porcupine.Unknown:
			unknown++
		case porcupine.Illegal:
			path := filepath.Join(out, fmt.Sprintf("lincheck-%s-%d.html", cfg.name, seed))
			if err := porcupine.VisualizePath(m, info, path); err != nil {
				t.Logf("visualization: %v", err)
			}
			t.Fatalf("history %d is not linearizable (visualization: %s)\n%s", histories, path, explain(m, history, info))
		}
	}
	t.Logf("%d histories of %d operations checked, %d timed out", histories, clients*ops, unknown)
	// A history that times out is not checked: if many do, the test checks nothing.
	if unknown*10 > histories {
		t.Fatalf("%d of %d histories timed out", unknown, histories)
	}
}

// explain describes where the check got stuck in each key's history that it could not
// linearize: the end of the longest order it found, and the operations that could not follow.
func explain(m porcupine.Model, history []porcupine.Operation, info porcupine.LinearizationInfo) string {
	var b strings.Builder
	describe := func(op porcupine.Operation) string {
		return fmt.Sprintf("client %d [%d, %d] %s", op.ClientId, op.Call, op.Return, m.DescribeOperation(op.Input, op.Output))
	}
	byKey := make(map[int][]porcupine.Operation)
	for _, op := range history {
		k := op.Input.(input).key
		byKey[k] = append(byKey[k], op)
	}
	for _, lins := range info.PartialLinearizationsOperations() {
		var longest []porcupine.Operation
		for _, l := range lins {
			if len(l) > len(longest) {
				longest = l
			}
		}
		if len(longest) == 0 {
			// Not even the first operation of the key could be ordered, so the key is unknown here:
			// show how every key starts.
			b.WriteString("no operation of a key could be ordered first; the keys start with\n")
			for key, ops := range byKey {
				sort.Slice(ops, func(i, j int) bool { return ops[i].Call < ops[j].Call })
				fmt.Fprintf(&b, "key %d:\n", key)
				for _, op := range ops[:min(len(ops), 10)] {
					fmt.Fprintf(&b, "  %s\n", describe(op))
				}
			}
			continue
		}
		key := longest[0].Input.(input).key
		ops := byKey[key]
		sort.Slice(ops, func(i, j int) bool { return ops[i].Call < ops[j].Call })
		if len(longest) == len(ops) {
			continue
		}
		placed := make(map[int64]bool, len(longest))
		for _, op := range longest {
			placed[op.Call] = true
		}
		fmt.Fprintf(&b, "key %d: %d of %d operations ordered; the order ends with\n", key, len(longest), len(ops))
		for _, op := range longest[max(0, len(longest)-10):] {
			fmt.Fprintf(&b, "  %s\n", describe(op))
		}
		// The operations that could come next: those called before the first unordered one returns.
		var next []porcupine.Operation
		first := int64(1 << 62)
		for _, op := range ops {
			if !placed[op.Call] {
				first = min(first, op.Return)
			}
		}
		for _, op := range ops {
			if !placed[op.Call] && op.Call < first {
				next = append(next, op)
			}
		}
		sort.Slice(next, func(i, j int) bool { return next[i].Call < next[j].Call })
		b.WriteString("and none of these can follow it:\n")
		for _, op := range next {
			fmt.Fprintf(&b, "  %s\n", describe(op))
		}
		// what ran around them, which the order above leaves out
		lo, hi := next[0].Call-100, next[len(next)-1].Return
		b.WriteString("all operations around them:\n")
		for _, op := range ops {
			if op.Return >= lo && op.Call <= hi {
				fmt.Fprintf(&b, "  %s\n", describe(op))
			}
		}
	}
	return b.String()
}
