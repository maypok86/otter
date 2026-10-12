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
// The cache has no size bound and its clock never moves, so nothing removes a key on its own,
// and every expiration and refresh time is the clock's time plus the duration a calculator or a
// setter gave it: the model is exact. Values written by the test are positive, values the loader
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
	opCount
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
}

// newRecorder returns a recorder for a cache with or without expiration. Without it, the cache
// replaces the node on every write; with it, a live entry is updated in place.
func newRecorder(keys int, expiry bool) *recorder {
	o := &otter.Options[int, int]{
		Clock:             frozenClock{},
		RefreshCalculator: otter.RefreshCreating[int, int](refreshableAfter * time.Hour),
		// failed refreshes are expected
		Logger: discardLogger{},
	}
	if expiry {
		o.ExpiryCalculator = otter.ExpiryCreating[int, int](expiresAfter * time.Hour)
	}
	return &recorder{
		cache: otter.Must(o),
		keys:  keys,
	}
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
func record(keys, clients, ops int, seed uint64, expiry bool) []porcupine.Operation {
	r := newRecorder(keys, expiry)
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

	var history []porcupine.Operation
	for _, h := range histories {
		history = append(history, h...)
	}
	return history
}

// TestLinearizability records histories until LINCHECK_DURATION (default 2s) has passed and
// checks each of them. A history that is not linearizable is written as an HTML visualization
// to LINCHECK_OUT (default the test's temporary directory).
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

	const (
		keys    = 2
		clients = 8
		ops     = 200
	)
	var models [2]porcupine.Model
	for i, expiry := range []bool{false, true} {
		nm := newModel(expiry)
		models[i] = nm.ToModel()
	}
	seed := uint64(time.Now().UnixNano())
	deadline := time.Now().Add(duration)
	histories, unknown := 0, 0
	for ; time.Now().Before(deadline); seed++ {
		// every other history is of a cache with expiration
		expiry := seed%2 == 1
		m := models[seed%2]
		history := record(keys, clients, ops, seed, expiry)
		histories++
		res, info := porcupine.CheckOperationsVerbose(m, history, 10*time.Second)
		switch res {
		case porcupine.Ok:
		case porcupine.Unknown:
			unknown++
		case porcupine.Illegal:
			path := filepath.Join(out, fmt.Sprintf("lincheck-%d.html", seed))
			if err := porcupine.VisualizePath(m, info, path); err != nil {
				t.Logf("visualization: %v", err)
			}
			t.Fatalf("history %d (expiry=%v) is not linearizable (visualization: %s)\n%s", histories, expiry, path, explain(m, history, info))
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
