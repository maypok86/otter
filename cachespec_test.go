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
	"io"
	"iter"
	"slices"
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
	// refreshCreating keeps the refresh time an entry was created with, so that a test can tell a
	// creation from an update.
	refreshCreating
)

func (k refreshKind) String() string {
	return [...]string{"noRefresh", "refreshWriting", "refreshCreating"}[k]
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

// populationKind is how many entries the cache holds when the test starts.
type populationKind int

const (
	emptyCache populationKind = iota
	// partialCache holds half of specMaximum entries, so that a few more fit without evictions.
	partialCache
	fullCache
)

func (k populationKind) String() string {
	return [...]string{"empty", "partial", "full"}[k]
}

func (k populationKind) size() int {
	return [...]int{0, specMaximum / 2, specMaximum}[k]
}

// executorKind is how the executor runs the cache's tasks.
type executorKind int

const (
	// queuedTasks keeps the tasks until the test calls run, so that it sees what is in flight.
	queuedTasks executorKind = iota
	// callerRuns runs each task on the goroutine that submits it.
	callerRuns
)

func (k executorKind) String() string {
	return [...]string{"queued", "callerRuns"}[k]
}

var (
	allMaximums = []maximumKind{unbounded, sizeBounded, weightBounded}
	allExpiries = []expiryKind{noExpiry, expiryWriting, expiryAccessing, expiryCreating}
	withExpiry  = []expiryKind{expiryWriting, expiryAccessing, expiryCreating}
	allRefresh  = []refreshKind{noRefresh, refreshWriting, refreshCreating}
	withRefresh = []refreshKind{refreshWriting, refreshCreating}
	allValues   = []valueKind{wordValues, pointerValues, boxedValues}
	// allPopulations is not the default: a spec that leaves the population out starts empty.
	allPopulations = []populationKind{emptyCache, partialCache, fullCache}
	// withRoom is the populations that leave room for a few more entries without evictions.
	withRoom = []populationKind{emptyCache, partialCache}
	// allExecutors is not the default either: a spec that leaves the executor out queues tasks.
	allExecutors = []executorKind{queuedTasks, callerRuns}
)

const (
	// specMaximum is the maximum size, or weight, of a bounded cache; every entry weighs 1.
	specMaximum = 10
	// specExpiry is the expiration duration of a cache that expires entries.
	specExpiry = time.Minute
	// specRefresh is the refresh duration of a cache that refreshes entries, shorter than
	// specExpiry so that an entry can be refreshed before it expires.
	specRefresh = 30 * time.Second
	// absentKey is a key that the population never holds.
	absentKey = 1000
)

// cacheSpec is the set of configurations a test holds for. An empty dimension means all of its
// values, except for the population, where it means an empty cache, and the executor, where it
// means queuedTasks.
type cacheSpec struct {
	maximum    []maximumKind
	expiry     []expiryKind
	refresh    []refreshKind
	values     []valueKind
	population []populationKind
	executor   []executorKind
	// logs allows the cache to log; otherwise a logged message fails the test.
	logs bool
	// weigher replaces the weigher of a cache bounded by weight, which weighs every entry 1.
	weigher func(key, value int) uint32
}

// cacheConfig is one combination of a cacheSpec.
type cacheConfig struct {
	maximum    maximumKind
	expiry     expiryKind
	refresh    refreshKind
	values     valueKind
	population populationKind
	executor   executorKind
}

func (c cacheConfig) String() string {
	return strings.Join([]string{
		c.maximum.String(), c.expiry.String(), c.refresh.String(), c.values.String(), c.population.String(),
		c.executor.String(),
	}, "/")
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
	populations := orAll(s.population, []populationKind{emptyCache})
	executors := orAll(s.executor, []executorKind{queuedTasks})
	configs := make([]cacheConfig, 0,
		len(maximums)*len(expiries)*len(refreshes)*len(valueKinds)*len(populations)*len(executors))
	for _, maximum := range maximums {
		for _, expiry := range expiries {
			for _, refresh := range refreshes {
				for _, values := range valueKinds {
					for _, population := range populations {
						for _, executor := range executors {
							configs = append(configs, cacheConfig{
								maximum: maximum, expiry: expiry, refresh: refresh, values: values,
								population: population, executor: executor,
							})
						}
					}
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

			test(t, newCacheContext(t, spec, cfg, newNonTickingClock()))
		})
	}
}

// cacheContext is a cache of one configuration over int keys and values, with a clock that moves
// only when told to, an executor that by default queues tasks until run is called (so an expired
// entry stays in the table until then), a stats counter, and recorders of the deletion listeners and the
// logger. The population is the keys 1 to its size, each with the value -key, written and
// processed by maintenance before the test starts.
type cacheContext struct {
	cacheConfig
	// spec is the spec of the test; its logs and weigher apply to every configuration.
	spec  cacheSpec
	clock *nonTickingClock
	stats *stats.Counter
	cache testCache
	// original is the population, which the test starts with.
	original map[int]int
	// onAtomicDeletion, if set, is called from OnAtomicDeletion after the event is recorded,
	// while the cache still holds the bucket lock, and onDeletion from OnDeletion.
	onAtomicDeletion func(e DeletionEvent[int, int])
	onDeletion       func(e DeletionEvent[int, int])

	queue           queuedExecutor
	atomicDeletions deletionRecorder
	deletions       deletionRecorder

	mu     sync.Mutex
	logged []string
}

func newCacheContext(t *testing.T, spec cacheSpec, cfg cacheConfig, clock *nonTickingClock) *cacheContext {
	t.Helper()

	cc := &cacheContext{
		cacheConfig: cfg, spec: spec, clock: clock, stats: stats.NewCounter(),
	}
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

	cc.original = make(map[int]int, cfg.population.size())
	for key := 1; key <= cfg.population.size(); key++ {
		cc.cache.Set(key, -key)
		cc.original[key] = -key
	}
	cc.run()
	t.Cleanup(func() {
		// A failed test has left the cache in a state the checks would only add noise to.
		if !t.Failed() {
			cc.check(t)
		}
	})
	return cc
}

// sibling returns an empty cache of the same configuration on the same clock, which is checked as
// the test ends, as cc is.
func (cc *cacheContext) sibling(t *testing.T) *cacheContext {
	t.Helper()

	cfg := cc.cacheConfig
	cfg.population = emptyCache
	return newCacheContext(t, cc.spec, cfg, cc.clock)
}

// keys returns the keys of the population in ascending order.
func (cc *cacheContext) keys() []int {
	keys := make([]int, 0, len(cc.original))
	for key := 1; key <= len(cc.original); key++ {
		keys = append(keys, key)
	}
	return keys
}

func (cc *cacheContext) submit(fn func()) {
	if cc.executor == callerRuns {
		fn()
		return
	}
	cc.queue.execute(fn)
}

// run runs the queued tasks, and those they queue, in order.
func (cc *cacheContext) run() {
	cc.queue.run()
}

// advance moves the clock forward.
func (cc *cacheContext) advance(d time.Duration) {
	cc.clock.Sleep(d)
}

// requireDeletions checks that both deletion listeners got exactly events, in order.
func (cc *cacheContext) requireDeletions(t *testing.T, events ...DeletionEvent[int, int]) {
	t.Helper()

	atomicDeletions, deletions := cc.atomicDeletions.get(), cc.deletions.get()
	if len(events) == 0 {
		require.Empty(t, atomicDeletions, "OnAtomicDeletion")
		require.Empty(t, deletions, "OnDeletion")
		return
	}
	require.Equal(t, events, atomicDeletions, "OnAtomicDeletion")
	require.Equal(t, events, deletions, "OnDeletion")
}

// requireDeletionsByKey checks that both deletion listeners got exactly events, those of each key
// in the given order and those of different keys in any order, for a call that removes entries
// in the order of the table or a test whose maintenance evicts them in the policy's order.
func (cc *cacheContext) requireDeletionsByKey(t *testing.T, events ...DeletionEvent[int, int]) {
	t.Helper()

	byKey := func(events []DeletionEvent[int, int]) map[int][]DeletionEvent[int, int] {
		m := make(map[int][]DeletionEvent[int, int])
		for _, e := range events {
			m[e.Key] = append(m[e.Key], e)
		}
		return m
	}
	atomicDeletions, deletions := cc.atomicDeletions.get(), cc.deletions.get()
	require.Equal(t, byKey(events), byKey(atomicDeletions), "OnAtomicDeletion")
	require.Equal(t, byKey(events), byKey(deletions), "OnDeletion")
}

// refreshNow refreshes the key 1 and runs the refresh.
func refreshNow(cc *cacheContext, loader Loader[int, int]) RefreshResult[int, int] {
	res := cc.cache.Refresh(1, loader)
	cc.run()
	return <-res
}

// requireValue checks that key holds want, or is absent if want is 0, without reading the entry
// through the cache, so that the check records no stats and no access.
func (cc *cacheContext) requireValue(t *testing.T, key, want int) {
	t.Helper()

	e, ok := cc.cache.GetEntryQuietly(key)
	v := e.Value
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

// requireLogged checks that the cache logged n messages.
func (cc *cacheContext) requireLogged(t *testing.T, n int) {
	t.Helper()

	cc.mu.Lock()
	defer cc.mu.Unlock()
	require.Len(t, cc.logged, n, "logged")
}

// loggedMessages returns what the cache logged, each as "level: message: error".
func (cc *cacheContext) loggedMessages() []string {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return slices.Clone(cc.logged)
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
	require.Zero(t, cc.queue.pending(), "tasks left queued")
	require.ElementsMatch(t, cc.atomicDeletions.get(), cc.deletions.get(), "the deletion listeners got different events")

	cc.mu.Lock()
	defer cc.mu.Unlock()
	if !cc.spec.logs {
		require.Empty(t, cc.logged, "logged")
	}
}

// testCache is the cache of a cacheContext with its values seen as ints, whatever their type.
type testCache interface {
	Set(key, value int) (int, bool)
	SetIfAbsent(key, value int) (int, bool)
	GetIfPresent(key int) (int, bool)
	GetEntryQuietly(key int) (Entry[int, int], bool)
	GetEntry(key int) (Entry[int, int], bool)
	Get(key int, loader Loader[int, int]) (int, error)
	GetContext(ctx context.Context, key int, loader Loader[int, int]) (int, error)
	BulkGet(keys []int, bulkLoader BulkLoader[int, int]) (map[int]int, error)
	BulkGetContext(ctx context.Context, keys []int, bulkLoader BulkLoader[int, int]) (map[int]int, error)
	// Refresh fails the test on a cache without refresh, whose Refresh returns a nil channel
	// that a receive would wait on forever.
	Refresh(key int, loader Loader[int, int]) <-chan RefreshResult[int, int]
	RefreshContext(ctx context.Context, key int, loader Loader[int, int]) <-chan RefreshResult[int, int]
	// BulkRefresh fails the test on a cache without refresh, as Refresh does.
	BulkRefresh(keys []int, bulkLoader BulkLoader[int, int]) <-chan []RefreshResult[int, int]
	BulkRefreshContext(
		ctx context.Context, keys []int, bulkLoader BulkLoader[int, int],
	) <-chan []RefreshResult[int, int]
	Invalidate(key int) (int, bool)
	Compute(key int, fn func(oldValue int, found bool) (int, ComputeOp)) (int, bool)
	ComputeIfAbsent(key int, fn func() (int, bool)) (int, bool)
	ComputeIfPresent(key int, fn func(oldValue int) (int, ComputeOp)) (int, bool)
	SetExpiresAfter(key int, expiresAfter time.Duration)
	SetRefreshableAfter(key int, refreshableAfter time.Duration)
	All() iter.Seq2[int, int]
	Keys() iter.Seq[int]
	Values() iter.Seq[int]
	InvalidateAll()
	EstimatedSize() int
	SaveTo(w io.Writer) error
	LoadFrom(r io.Reader) error
	WeightedSize() uint64
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
			cc.atomicDeletions.record(event(e))
			if cc.onAtomicDeletion != nil {
				cc.onAtomicDeletion(event(e))
			}
		},
		OnDeletion: func(e DeletionEvent[int, V]) {
			cc.deletions.record(event(e))
			if cc.onDeletion != nil {
				cc.onDeletion(event(e))
			}
		},
	}
	switch cc.maximum {
	case sizeBounded:
		o.MaximumSize = specMaximum
	case weightBounded:
		o.MaximumWeight = specMaximum
		o.Weigher = func(int, V) uint32 { return 1 }
		if weigher := cc.spec.weigher; weigher != nil {
			o.Weigher = func(key int, value V) uint32 { return weigher(key, fromV(value)) }
		}
	}
	switch cc.expiry {
	case expiryWriting:
		o.ExpiryCalculator = ExpiryWriting[int, V](specExpiry)
	case expiryAccessing:
		o.ExpiryCalculator = ExpiryAccessing[int, V](specExpiry)
	case expiryCreating:
		o.ExpiryCalculator = ExpiryCreating[int, V](specExpiry)
	}
	switch cc.refresh {
	case refreshWriting:
		o.RefreshCalculator = RefreshWriting[int, V](specRefresh)
	case refreshCreating:
		o.RefreshCalculator = RefreshCreating[int, V](specRefresh)
	}
	c, err := New(o)
	require.NoError(t, err)
	return &specCache[V]{t: t, c: c, toV: toV, fromV: fromV}
}

// specLoader is a Loader of values of type V over a Loader of ints, which keeps loads and
// reloads apart.
type specLoader[V any] struct {
	s *specCache[V]
	l Loader[int, int]
}

func (sl specLoader[V]) Load(ctx context.Context, key int) (V, error) {
	v, err := sl.l.Load(ctx, key)
	return sl.s.toV(v), err
}

func (sl specLoader[V]) Reload(ctx context.Context, key int, oldValue V) (V, error) {
	v, err := sl.l.Reload(ctx, key, sl.s.fromV(oldValue))
	return sl.s.toV(v), err
}

// specBulkLoader is a BulkLoader of values of type V over a BulkLoader of ints.
type specBulkLoader[V any] struct {
	s  *specCache[V]
	bl BulkLoader[int, int]
}

func (sl specBulkLoader[V]) convert(m map[int]int, err error) (map[int]V, error) {
	if m == nil {
		return nil, err
	}
	res := make(map[int]V, len(m))
	for k, v := range m {
		res[k] = sl.s.toV(v)
	}
	return res, err
}

func (sl specBulkLoader[V]) BulkLoad(ctx context.Context, keys []int) (map[int]V, error) {
	return sl.convert(sl.bl.BulkLoad(ctx, keys))
}

func (sl specBulkLoader[V]) BulkReload(ctx context.Context, keys []int, oldValues []V) (map[int]V, error) {
	old := make([]int, 0, len(oldValues))
	for _, v := range oldValues {
		old = append(old, sl.s.fromV(v))
	}
	return sl.convert(sl.bl.BulkReload(ctx, keys, old))
}

func (s *specCache[V]) loader(l Loader[int, int]) Loader[int, V] {
	return specLoader[V]{s: s, l: l}
}

func (s *specCache[V]) bulkLoader(bl BulkLoader[int, int]) BulkLoader[int, V] {
	return specBulkLoader[V]{s: s, bl: bl}
}

func (s *specCache[V]) fromMap(m map[int]V) map[int]int {
	if m == nil {
		return nil
	}
	res := make(map[int]int, len(m))
	for k, v := range m {
		res[k] = s.fromV(v)
	}
	return res
}

func (s *specCache[V]) Set(key, value int) (int, bool) {
	v, ok := s.c.Set(key, s.toV(value))
	return s.fromV(v), ok
}

func (s *specCache[V]) SetIfAbsent(key, value int) (int, bool) {
	v, ok := s.c.SetIfAbsent(key, s.toV(value))
	return s.fromV(v), ok
}

func (s *specCache[V]) GetIfPresent(key int) (int, bool) {
	v, ok := s.c.GetIfPresent(key)
	return s.fromV(v), ok
}

func (s *specCache[V]) GetEntryQuietly(key int) (Entry[int, int], bool) {
	e, ok := s.c.GetEntryQuietly(key)
	return s.entry(e), ok
}

func (s *specCache[V]) GetEntry(key int) (Entry[int, int], bool) {
	e, ok := s.c.GetEntry(key)
	return s.entry(e), ok
}

func (s *specCache[V]) entry(e Entry[int, V]) Entry[int, int] {
	return Entry[int, int]{
		Key:               e.Key,
		Value:             s.fromV(e.Value),
		Weight:            e.Weight,
		ExpiresAtNano:     e.ExpiresAtNano,
		RefreshableAtNano: e.RefreshableAtNano,
		SnapshotAtNano:    e.SnapshotAtNano,
	}
}

func (s *specCache[V]) Get(key int, loader Loader[int, int]) (int, error) {
	return s.GetContext(context.Background(), key, loader)
}

func (s *specCache[V]) GetContext(ctx context.Context, key int, loader Loader[int, int]) (int, error) {
	v, err := s.c.Get(ctx, key, s.loader(loader))
	return s.fromV(v), err
}

func (s *specCache[V]) BulkGet(keys []int, bulkLoader BulkLoader[int, int]) (map[int]int, error) {
	return s.BulkGetContext(context.Background(), keys, bulkLoader)
}

func (s *specCache[V]) BulkGetContext(
	ctx context.Context, keys []int, bulkLoader BulkLoader[int, int],
) (map[int]int, error) {
	m, err := s.c.BulkGet(ctx, keys, s.bulkLoader(bulkLoader))
	return s.fromMap(m), err
}

func (s *specCache[V]) Refresh(key int, loader Loader[int, int]) <-chan RefreshResult[int, int] {
	return s.RefreshContext(context.Background(), key, loader)
}

func (s *specCache[V]) RefreshContext(
	ctx context.Context, key int, loader Loader[int, int],
) <-chan RefreshResult[int, int] {
	res := s.c.Refresh(ctx, key, s.loader(loader))
	require.NotNil(s.t, res, "Refresh of a cache without refresh: restrict the spec to withRefresh")
	ch := make(chan RefreshResult[int, int], 1)
	go func() {
		r := <-res
		ch <- RefreshResult[int, int]{Key: r.Key, Value: s.fromV(r.Value), Err: r.Err}
	}()
	return ch
}

func (s *specCache[V]) BulkRefresh(keys []int, bulkLoader BulkLoader[int, int]) <-chan []RefreshResult[int, int] {
	return s.BulkRefreshContext(context.Background(), keys, bulkLoader)
}

func (s *specCache[V]) BulkRefreshContext(
	ctx context.Context, keys []int, bulkLoader BulkLoader[int, int],
) <-chan []RefreshResult[int, int] {
	res := s.c.BulkRefresh(ctx, keys, s.bulkLoader(bulkLoader))
	require.NotNil(s.t, res, "BulkRefresh of a cache without refresh: restrict the spec to withRefresh")
	ch := make(chan []RefreshResult[int, int], 1)
	go func() {
		rs := <-res
		converted := make([]RefreshResult[int, int], 0, len(rs))
		for _, r := range rs {
			converted = append(converted, RefreshResult[int, int]{Key: r.Key, Value: s.fromV(r.Value), Err: r.Err})
		}
		ch <- converted
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

func (s *specCache[V]) SetExpiresAfter(key int, expiresAfter time.Duration) {
	s.c.SetExpiresAfter(key, expiresAfter)
}

func (s *specCache[V]) SetRefreshableAfter(key int, refreshableAfter time.Duration) {
	s.c.SetRefreshableAfter(key, refreshableAfter)
}

func (s *specCache[V]) All() iter.Seq2[int, int] {
	return func(yield func(int, int) bool) {
		for k, v := range s.c.All() {
			if !yield(k, s.fromV(v)) {
				return
			}
		}
	}
}

func (s *specCache[V]) Keys() iter.Seq[int] {
	return s.c.Keys()
}

func (s *specCache[V]) Values() iter.Seq[int] {
	return func(yield func(int) bool) {
		for v := range s.c.Values() {
			if !yield(s.fromV(v)) {
				return
			}
		}
	}
}

func (s *specCache[V]) InvalidateAll() {
	s.c.InvalidateAll()
}

func (s *specCache[V]) SaveTo(w io.Writer) error {
	return SaveCacheTo(s.c, w)
}

func (s *specCache[V]) LoadFrom(r io.Reader) error {
	return LoadCacheFrom(s.c, r)
}

func (s *specCache[V]) EstimatedSize() int {
	return s.c.EstimatedSize()
}

func (s *specCache[V]) WeightedSize() uint64 {
	return s.c.WeightedSize()
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

// queuedExecutor holds the submitted tasks until run is called, so that a test decides when a
// refresh starts.
type queuedExecutor struct {
	mu    sync.Mutex
	tasks []func()
}

func (e *queuedExecutor) execute(fn func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tasks = append(e.tasks, fn)
}

// pending returns the number of queued tasks.
func (e *queuedExecutor) pending() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.tasks)
}

// run runs the queued tasks, including the ones they submit, until none are left.
func (e *queuedExecutor) run() {
	for {
		e.mu.Lock()
		tasks := e.tasks
		e.tasks = nil
		e.mu.Unlock()
		if len(tasks) == 0 {
			return
		}
		for _, task := range tasks {
			task()
		}
	}
}

// deletionRecorder records the events of a deletion listener.
type deletionRecorder struct {
	mu     sync.Mutex
	events []DeletionEvent[int, int]
}

func (r *deletionRecorder) record(e DeletionEvent[int, int]) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *deletionRecorder) get() []DeletionEvent[int, int] {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}
