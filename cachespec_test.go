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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/stats"
)

// A test that depends on the configuration of the cache declares the configurations it holds for
// in a cacheSpec, and forEachSpec runs it on every combination of them, each as a subtest named
// after the combination (maximum/expiry/refresh/value), on a cacheContext that records what the
// cache did and checks the cache once the test is done.
//
// The dimensions are the features that select the generated node type (the bound, expiration and
// refresh) and the value storage the node keeps its value in, so that the combinations cover
// every node type with every value storage but that of zero-size values, which a test that sees
// values as ints cannot tell apart (storage_test.go covers it).

type maximumKind int

const (
	unbounded maximumKind = iota
	sizeBounded
	weightBounded
)

func (k maximumKind) String() string {
	return [...]string{"unbounded", "size", "weight"}[k]
}

type expiryKind int

const (
	noExpiry expiryKind = iota
	expiryWriting
	expiryAccessing
	expiryCreating
)

func (k expiryKind) String() string {
	return [...]string{"noExpiry", "writing", "accessing", "creating"}[k]
}

type refreshKind int

const (
	noRefresh refreshKind = iota
	refreshWriting
)

func (k refreshKind) String() string {
	return [...]string{"noRefresh", "refresh"}[k]
}

// valueKind is a type of value, by the storage its node keeps it in. A test sees values as ints,
// which the kind converts to and from its type.
type valueKind int

const (
	// wordValues are kept in one atomic word and updated in place.
	wordValues valueKind = iota
	// pointerValues are kept as one atomic pointer and updated in place.
	pointerValues
	// boxedValues do not fit in a word: the node is replaced by a boxed one on the first update.
	boxedValues
)

func (k valueKind) String() string {
	return [...]string{"word", "pointer", "boxed"}[k]
}

// inPlace reports whether a node can keep a value of the kind when it is updated.
func (k valueKind) inPlace() bool {
	return k != boxedValues
}

var (
	allMaximums = []maximumKind{unbounded, sizeBounded, weightBounded}
	allExpiries = []expiryKind{noExpiry, expiryWriting, expiryAccessing, expiryCreating}
	allRefresh  = []refreshKind{noRefresh, refreshWriting}
	allValues   = []valueKind{wordValues, pointerValues, boxedValues}
)

const (
	// specMaximum is the maximum size, or weight, of a bounded cache; every entry weighs 1.
	specMaximum = 10
	// specExpiry is the expiration duration of a cache that expires entries.
	specExpiry = time.Minute
	// specRefresh is the refresh duration of a cache that refreshes entries.
	specRefresh = time.Hour
)

// cacheSpec is the set of configurations a test holds for. An empty dimension means all of its
// values.
type cacheSpec struct {
	maximum []maximumKind
	expiry  []expiryKind
	refresh []refreshKind
	values  []valueKind
	// logs allows the cache to log; otherwise a logged message fails the test.
	logs bool
}

// cacheConfig is one combination of a cacheSpec.
type cacheConfig struct {
	maximum maximumKind
	expiry  expiryKind
	refresh refreshKind
	values  valueKind
}

func (c cacheConfig) String() string {
	return strings.Join([]string{c.maximum.String(), c.expiry.String(), c.refresh.String(), c.values.String()}, "/")
}

// maintained reports whether the nodes of the cache keep state. The nodes of a cache that
// neither bounds nor expires entries keep none, and an update always replaces them.
func (c cacheConfig) maintained() bool {
	return c.maximum != unbounded || c.expiry != noExpiry
}

// updatesInPlace reports whether the first update of a live entry is applied to its node. After
// it, the node of a maintained cache can be updated in place whatever the value is.
func (c cacheConfig) updatesInPlace() bool {
	return c.values.inPlace() && c.maintained()
}

func orAll[T any](dimension, all []T) []T {
	if len(dimension) == 0 {
		return all
	}
	return dimension
}

func (s cacheSpec) configs() []cacheConfig {
	maximums, expiries := orAll(s.maximum, allMaximums), orAll(s.expiry, allExpiries)
	refreshes, valueKinds := orAll(s.refresh, allRefresh), orAll(s.values, allValues)
	configs := make([]cacheConfig, 0, len(maximums)*len(expiries)*len(refreshes)*len(valueKinds))
	for _, maximum := range maximums {
		for _, expiry := range expiries {
			for _, refresh := range refreshes {
				for _, values := range valueKinds {
					configs = append(configs, cacheConfig{maximum: maximum, expiry: expiry, refresh: refresh, values: values})
				}
			}
		}
	}
	return configs
}

// forEachSpec runs test on a new cache of every configuration of spec, in parallel subtests.
func forEachSpec(t *testing.T, spec cacheSpec, test func(t *testing.T, cc *cacheContext)) {
	t.Helper()

	configs := spec.configs()
	require.NotEmpty(t, configs, "the spec has no configurations")
	for _, cfg := range configs {
		t.Run(cfg.String(), func(t *testing.T) {
			t.Parallel()

			cc := newCacheContext(t, spec, cfg)
			t.Cleanup(func() {
				// A failed test has left the cache in a state the checks would only add noise to.
				if !t.Failed() {
					cc.check(t)
				}
			})
			test(t, cc)
		})
	}
}

// cacheContext is a cache of one configuration over int keys and values, with a clock that moves
// only when told to, an executor that queues tasks until run is called (so an expired entry stays
// in the table until then), a stats counter, and recorders of the deletion listeners and the
// logger.
type cacheContext struct {
	cacheConfig
	// logs allows the cache to log, from the spec.
	logs  bool
	clock *nonTickingClock
	stats *stats.Counter
	cache testCache
	// onAtomicDeletion, if set, is called from OnAtomicDeletion after the event is recorded,
	// while the cache still holds the bucket lock.
	onAtomicDeletion func(e DeletionEvent[int, int])

	mu              sync.Mutex
	pending         []func()
	atomicDeletions []DeletionEvent[int, int]
	deletions       []DeletionEvent[int, int]
	logged          []string
}

func newCacheContext(t *testing.T, spec cacheSpec, cfg cacheConfig) *cacheContext {
	t.Helper()

	cc := &cacheContext{cacheConfig: cfg, logs: spec.logs, clock: newNonTickingClock(), stats: stats.NewCounter()}
	switch cfg.values {
	case wordValues:
		cc.cache = newSpecCache(t, cc, func(v int) int { return v }, func(v int) int { return v })
	case pointerValues:
		cc.cache = newSpecCache(t, cc, func(v int) *int { return &v }, func(v *int) int {
			if v == nil {
				return 0
			}
			return *v
		})
	case boxedValues:
		cc.cache = newSpecCache(t, cc,
			func(v int) [2]int64 { return [2]int64{int64(v), int64(v)} },
			func(v [2]int64) int { return int(v[0]) })
	}
	return cc
}

func (cc *cacheContext) submit(fn func()) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.pending = append(cc.pending, fn)
}

// run runs the queued tasks, and those they queue, in order.
func (cc *cacheContext) run() {
	for {
		cc.mu.Lock()
		if len(cc.pending) == 0 {
			cc.mu.Unlock()
			return
		}
		fn := cc.pending[0]
		cc.pending = cc.pending[1:]
		cc.mu.Unlock()
		fn()
	}
}

func (cc *cacheContext) pendingTasks() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return len(cc.pending)
}

// advance moves the clock forward.
func (cc *cacheContext) advance(d time.Duration) {
	cc.clock.Sleep(d)
}

// record appends a deletion event to the events of one listener.
func (cc *cacheContext) record(events *[]DeletionEvent[int, int], e DeletionEvent[int, int]) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	*events = append(*events, e)
}

// requireDeletions checks that both deletion listeners got exactly events, in order.
func (cc *cacheContext) requireDeletions(t *testing.T, events ...DeletionEvent[int, int]) {
	t.Helper()

	cc.mu.Lock()
	atomicDeletions, deletions := cc.atomicDeletions, cc.deletions
	cc.mu.Unlock()
	require.Equal(t, events, atomicDeletions, "OnAtomicDeletion")
	require.Equal(t, events, deletions, "OnDeletion")
}

// refreshNow refreshes the key 1 and runs the refresh.
func refreshNow(cc *cacheContext, loader Loader[int, int]) RefreshResult[int, int] {
	res := cc.cache.Refresh(1, loader)
	cc.run()
	return <-res
}

// requireValue checks that key holds want, or is absent if want is 0.
func (cc *cacheContext) requireValue(t *testing.T, key, want int) {
	t.Helper()

	v, ok := cc.cache.GetIfPresent(key)
	if want == 0 {
		require.False(t, ok, "the key holds %d", v)
		return
	}
	require.True(t, ok, "the key is absent")
	require.Equal(t, want, v)
}

func (cc *cacheContext) Warn(_ context.Context, msg string, err error) {
	cc.log("warn", msg, err)
}

func (cc *cacheContext) Error(_ context.Context, msg string, err error) {
	cc.log("error", msg, err)
}

func (cc *cacheContext) log(level, msg string, err error) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.logged = append(cc.logged, level+": "+msg+": "+err.Error())
}

// check runs what the cache has left to do and checks what every test must leave behind: the
// cache is consistent, no task is left queued, both deletion listeners got the same events, and
// nothing was logged unless the spec allows it.
func (cc *cacheContext) check(t *testing.T) {
	t.Helper()

	cc.cache.CleanUp()
	cc.run()
	cc.cache.validate(t)
	require.Zero(t, cc.pendingTasks(), "tasks left queued")

	cc.mu.Lock()
	defer cc.mu.Unlock()
	require.ElementsMatch(t, cc.atomicDeletions, cc.deletions, "the deletion listeners got different events")
	if !cc.logs {
		require.Empty(t, cc.logged, "logged")
	}
}

// testCache is the cache of a cacheContext with its values seen as ints, whatever their type.
type testCache interface {
	Set(key, value int)
	SetIfAbsent(key, value int) (int, bool)
	GetIfPresent(key int) (int, bool)
	Get(key int, loader Loader[int, int]) (int, error)
	// Refresh fails the test on a cache without refresh, whose Refresh returns a nil channel
	// that a receive would wait on forever.
	Refresh(key int, loader Loader[int, int]) <-chan RefreshResult[int, int]
	Invalidate(key int) (int, bool)
	Compute(key int, fn func(oldValue int, found bool) (int, ComputeOp)) (int, bool)
	ComputeIfAbsent(key int, fn func() (int, bool)) (int, bool)
	ComputeIfPresent(key int, fn func(oldValue int) (int, ComputeOp)) (int, bool)
	CleanUp()

	// nodeID identifies the node of key, or is nil if the table has none.
	nodeID(key int) any
	// peek returns the value of the node of key as a lock-free reader sees it, without reading
	// it through the cache.
	peek(key int) (int, bool)
	validate(t *testing.T)
}

// specCache is a testCache over values of type V.
type specCache[V any] struct {
	t     *testing.T
	c     *Cache[int, V]
	toV   func(int) V
	fromV func(V) int
}

func newSpecCache[V any](t *testing.T, cc *cacheContext, toV func(int) V, fromV func(V) int) *specCache[V] {
	t.Helper()

	event := func(e DeletionEvent[int, V]) DeletionEvent[int, int] {
		return DeletionEvent[int, int]{Key: e.Key, Value: fromV(e.Value), Cause: e.Cause}
	}
	o := &Options[int, V]{
		Clock:         cc.clock,
		Executor:      cc.submit,
		Logger:        cc,
		StatsRecorder: cc.stats,
		OnAtomicDeletion: func(e DeletionEvent[int, V]) {
			cc.record(&cc.atomicDeletions, event(e))
			if cc.onAtomicDeletion != nil {
				cc.onAtomicDeletion(event(e))
			}
		},
		OnDeletion: func(e DeletionEvent[int, V]) {
			cc.record(&cc.deletions, event(e))
		},
	}
	switch cc.maximum {
	case sizeBounded:
		o.MaximumSize = specMaximum
	case weightBounded:
		o.MaximumWeight = specMaximum
		o.Weigher = func(int, V) uint32 { return 1 }
	}
	switch cc.expiry {
	case expiryWriting:
		o.ExpiryCalculator = ExpiryWriting[int, V](specExpiry)
	case expiryAccessing:
		o.ExpiryCalculator = ExpiryAccessing[int, V](specExpiry)
	case expiryCreating:
		o.ExpiryCalculator = ExpiryCreating[int, V](specExpiry)
	}
	if cc.refresh == refreshWriting {
		o.RefreshCalculator = RefreshWriting[int, V](specRefresh)
	}
	c, err := New(o)
	require.NoError(t, err)
	return &specCache[V]{t: t, c: c, toV: toV, fromV: fromV}
}

func (s *specCache[V]) loader(l Loader[int, int]) Loader[int, V] {
	return LoaderFunc[int, V](func(ctx context.Context, key int) (V, error) {
		v, err := l.Load(ctx, key)
		return s.toV(v), err
	})
}

func (s *specCache[V]) Set(key, value int) {
	s.c.Set(key, s.toV(value))
}

func (s *specCache[V]) SetIfAbsent(key, value int) (int, bool) {
	v, ok := s.c.SetIfAbsent(key, s.toV(value))
	return s.fromV(v), ok
}

func (s *specCache[V]) GetIfPresent(key int) (int, bool) {
	v, ok := s.c.GetIfPresent(key)
	return s.fromV(v), ok
}

func (s *specCache[V]) Get(key int, loader Loader[int, int]) (int, error) {
	v, err := s.c.Get(context.Background(), key, s.loader(loader))
	return s.fromV(v), err
}

func (s *specCache[V]) Refresh(key int, loader Loader[int, int]) <-chan RefreshResult[int, int] {
	res := s.c.Refresh(context.Background(), key, s.loader(loader))
	require.NotNil(s.t, res, "Refresh of a cache without refresh: restrict the spec to refreshWriting")
	ch := make(chan RefreshResult[int, int], 1)
	go func() {
		r := <-res
		ch <- RefreshResult[int, int]{Key: r.Key, Value: s.fromV(r.Value), Err: r.Err}
	}()
	return ch
}

func (s *specCache[V]) Invalidate(key int) (int, bool) {
	v, ok := s.c.Invalidate(key)
	return s.fromV(v), ok
}

func (s *specCache[V]) Compute(key int, fn func(oldValue int, found bool) (int, ComputeOp)) (int, bool) {
	v, ok := s.c.Compute(key, func(oldValue V, found bool) (V, ComputeOp) {
		nv, op := fn(s.fromV(oldValue), found)
		return s.toV(nv), op
	})
	return s.fromV(v), ok
}

func (s *specCache[V]) ComputeIfAbsent(key int, fn func() (int, bool)) (int, bool) {
	v, ok := s.c.ComputeIfAbsent(key, func() (V, bool) {
		nv, cancel := fn()
		return s.toV(nv), cancel
	})
	return s.fromV(v), ok
}

func (s *specCache[V]) ComputeIfPresent(key int, fn func(oldValue int) (int, ComputeOp)) (int, bool) {
	v, ok := s.c.ComputeIfPresent(key, func(oldValue V) (V, ComputeOp) {
		nv, op := fn(s.fromV(oldValue))
		return s.toV(nv), op
	})
	return s.fromV(v), ok
}

func (s *specCache[V]) CleanUp() {
	s.c.CleanUp()
}

func (s *specCache[V]) nodeID(key int) any {
	n := s.c.cache.hashmap.Get(key)
	if n == nil {
		return nil
	}
	return n.AsPointer()
}

func (s *specCache[V]) peek(key int) (int, bool) {
	n := s.c.cache.hashmap.Get(key)
	if n == nil {
		return 0, false
	}
	return s.fromV(n.Value()), true
}

func (s *specCache[V]) validate(t *testing.T) {
	t.Helper()
	validateCache(t, s.c)
}
