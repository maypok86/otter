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
	"fmt"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

// testBulkLoader records the batches it loads and reloads. Every call waits until release is
// closed, if it is set, and then runs fn, which by default returns key*10 for every key.
type testBulkLoader struct {
	release chan struct{}
	fn      func(ctx context.Context, keys []int) (map[int]int, error)

	mu         sync.Mutex
	batches    [][]int
	reloads    [][]int
	oldValues  [][]int
	kinds      []string
	running    int
	maxRunning int
}

func (tl *testBulkLoader) BulkLoad(ctx context.Context, keys []int) (map[int]int, error) {
	tl.mu.Lock()
	tl.batches = append(tl.batches, slices.Clone(keys))
	tl.kinds = append(tl.kinds, "load")
	tl.mu.Unlock()
	return tl.call(ctx, keys)
}

func (tl *testBulkLoader) BulkReload(ctx context.Context, keys, oldValues []int) (map[int]int, error) {
	tl.mu.Lock()
	tl.reloads = append(tl.reloads, slices.Clone(keys))
	tl.oldValues = append(tl.oldValues, slices.Clone(oldValues))
	tl.kinds = append(tl.kinds, "reload")
	tl.mu.Unlock()
	return tl.call(ctx, keys)
}

func (tl *testBulkLoader) call(ctx context.Context, keys []int) (map[int]int, error) {
	tl.mu.Lock()
	tl.running++
	tl.maxRunning = max(tl.maxRunning, tl.running)
	tl.mu.Unlock()
	defer func() {
		tl.mu.Lock()
		tl.running--
		tl.mu.Unlock()
	}()

	if tl.release != nil {
		<-tl.release
	}
	if tl.fn != nil {
		return tl.fn(ctx, keys)
	}
	res := make(map[int]int, len(keys))
	for _, k := range keys {
		res[k] = k * 10
	}
	return res, nil
}

func (tl *testBulkLoader) batchSizes() []int {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	sizes := make([]int, 0, len(tl.batches))
	for _, b := range tl.batches {
		sizes = append(sizes, len(b))
	}
	return sizes
}

func (tl *testBulkLoader) runningCalls() int {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	return tl.running
}

// waitFor waits until cond holds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not met")
		}
		runtime.Gosched()
	}
}

// queued returns the number of keys waiting for a batch.
func (l *Loader[K, V]) queued() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.loads.waiting) + len(l.reloads.waiting)
}

func (l *Loader[K, V]) idle() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inFlight == 0 &&
		len(l.loads.waiting) == 0 && len(l.loads.requests) == 0 &&
		len(l.reloads.waiting) == 0 && len(l.reloads.requests) == 0
}

// loadAll calls Load for every key concurrently and checks every result.
func loadAll(t *testing.T, l *Loader[int, int], keys []int) *sync.WaitGroup {
	t.Helper()
	var wg sync.WaitGroup
	for _, k := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := l.Load(context.Background(), k)
			if err != nil || v != k*10 {
				t.Errorf("Load(%d) = %d, %v; want %d, nil", k, v, err, k*10)
			}
		}()
	}
	return &wg
}

func keysUpTo(n int) []int {
	keys := make([]int, n)
	for i := range keys {
		keys[i] = i
	}
	return keys
}

func TestNew(t *testing.T) {
	t.Parallel()

	tl := &testBulkLoader{}
	_, err := New[int, int](nil, nil)
	require.Error(t, err)
	_, err = New[int, int](tl, &Options{MaxBatchSize: -1})
	require.Error(t, err)
	_, err = New[int, int](tl, &Options{MaxDelay: -time.Second})
	require.Error(t, err)
	require.Panics(t, func() {
		Must[int, int](nil, nil)
	})

	for _, tt := range []struct {
		loadsPerReload int
		want           int
	}{
		{loadsPerReload: 0, want: 1},
		{loadsPerReload: 4, want: 4},
		{loadsPerReload: -1, want: -1},
		{loadsPerReload: -3, want: -1},
	} {
		l := Must[int, int](tl, &Options{LoadsPerReload: tt.loadsPerReload})
		require.Equal(t, tt.want, l.loadsPerReload, "LoadsPerReload %d", tt.loadsPerReload)
	}

	for _, tt := range []struct {
		maxInFlight int
		want        int
	}{
		{maxInFlight: 0, want: 1},
		{maxInFlight: 3, want: 3},
		{maxInFlight: -1, want: -1},
		{maxInFlight: -5, want: -1},
	} {
		l := Must[int, int](tl, &Options{MaxInFlight: tt.maxInFlight})
		require.Equal(t, tt.want, l.maxInFlight, "MaxInFlight %d", tt.maxInFlight)
	}
}

func TestLoader_Batches(t *testing.T) {
	t.Parallel()

	// The first keys leave at once, one per free call; the keys that come while every call is
	// busy leave together when one finishes, cut to MaxBatchSize.
	tests := []struct {
		name    string
		options Options
		keys    int
		busy    int
		want    []int
	}{
		{name: "one call", keys: 100, busy: 1, want: []int{1, 99}},
		{name: "two calls", options: Options{MaxInFlight: 2}, keys: 100, busy: 2, want: []int{1, 1, 98}},
		{name: "batch size", options: Options{MaxBatchSize: 10}, keys: 31, busy: 1, want: []int{1, 10, 10, 10}},
		{name: "no limit", options: Options{MaxInFlight: -1}, keys: 20, busy: 20, want: slices.Repeat([]int{1}, 20)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tl := &testBulkLoader{release: make(chan struct{})}
			l := Must[int, int](tl, &tt.options)
			wg := loadAll(t, l, keysUpTo(tt.keys))
			waitFor(t, func() bool {
				return tl.runningCalls() == tt.busy && l.queued() == tt.keys-tt.busy
			})
			close(tl.release)
			wg.Wait()

			require.True(t, l.idle())
			sizes := tl.batchSizes()
			slices.Sort(sizes)
			require.Equal(t, tt.want, sizes)
			require.Equal(t, tt.busy, tl.maxRunning)
		})
	}
}

func TestLoader_DuplicateKeys(t *testing.T) {
	t.Parallel()

	tl := &testBulkLoader{release: make(chan struct{})}
	l := Must[int, int](tl, nil)
	wg := loadAll(t, l, []int{0})
	waitFor(t, func() bool { return tl.runningCalls() == 1 })
	wg2 := loadAll(t, l, []int{7, 7})
	waitFor(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		r := l.loads.waiting[7]
		return r != nil && r.callers == 2
	})
	close(tl.release)
	wg.Wait()
	wg2.Wait()

	require.Equal(t, [][]int{{0}, {7}}, tl.batches)
}

func TestLoader_Context(t *testing.T) {
	t.Parallel()

	type ctxKey struct{}
	release := make(chan struct{})
	batchCtx := make(chan context.Context, 1)
	finish := make(chan struct{})
	tl := &testBulkLoader{fn: func(ctx context.Context, keys []int) (map[int]int, error) {
		if keys[0] == 0 {
			<-release
			return map[int]int{0: 0}, nil
		}
		batchCtx <- ctx
		<-finish
		return map[int]int{1: 10, 2: 20}, ctx.Err()
	}}
	l := Must[int, int](tl, nil)

	go func() {
		_, _ = l.Load(context.Background(), 0)
	}()
	waitFor(t, func() bool { return tl.runningCalls() == 1 })

	// Keys 1 and 2 wait for the next batch; key 1 comes first, so the batch runs with the
	// values of its context.
	ctx1, cancel1 := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "first"))
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "second"))
	defer cancel2()
	res1 := make(chan error, 1)
	go func() {
		_, err := l.Load(ctx1, 1)
		res1 <- err
	}()
	waitFor(t, func() bool { return l.queued() == 1 })
	res2 := make(chan error, 1)
	go func() {
		_, err := l.Load(ctx2, 2)
		res2 <- err
	}()
	waitFor(t, func() bool { return l.queued() == 2 })
	close(release)

	ctx := <-batchCtx
	require.Equal(t, "first", ctx.Value(ctxKey{}))

	// The first caller gives up: it returns at once, and the batch goes on for the second.
	cancel1()
	require.ErrorIs(t, <-res1, context.Canceled)
	require.NoError(t, ctx.Err())

	// Nobody waits for the batch any more: it is cancelled.
	cancel2()
	require.ErrorIs(t, <-res2, context.Canceled)
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the batch is not cancelled after every caller has returned")
	}
	close(finish)
	waitFor(t, l.idle)
}

func TestLoader_AbandonedKeyIsNotLoaded(t *testing.T) {
	t.Parallel()

	tl := &testBulkLoader{release: make(chan struct{})}
	l := Must[int, int](tl, nil)
	wg := loadAll(t, l, []int{0})
	waitFor(t, func() bool { return tl.runningCalls() == 1 })

	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		_, err := l.Load(ctx, 5)
		res <- err
	}()
	waitFor(t, func() bool { return l.queued() == 1 })
	cancel()
	require.ErrorIs(t, <-res, context.Canceled)
	require.Zero(t, l.queued())

	// The key is queued again by a later call and loaded then.
	wg2 := loadAll(t, l, []int{5})
	close(tl.release)
	wg.Wait()
	wg2.Wait()
	waitFor(t, l.idle)
	require.Equal(t, [][]int{{0}, {5}}, tl.batches)
}

func TestLoader_CancelledContext(t *testing.T) {
	t.Parallel()

	tl := &testBulkLoader{}
	l := Must[int, int](tl, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := l.Load(ctx, 1)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, tl.batches)
}

func TestLoader_Results(t *testing.T) {
	t.Parallel()

	errBatch := errors.New("batch failed")
	tests := []struct {
		name  string
		fn    func(ctx context.Context, keys []int) (map[int]int, error)
		check func(t *testing.T, key int, load func() (int, error))
	}{
		{
			name: "omitted key is not found",
			fn: func(_ context.Context, keys []int) (map[int]int, error) {
				res := make(map[int]int)
				for _, k := range keys {
					if k%2 == 0 {
						res[k] = k * 10
					}
				}
				return res, nil
			},
			check: func(t *testing.T, key int, load func() (int, error)) {
				t.Helper()
				v, err := load()
				if key%2 == 0 {
					require.NoError(t, err)
					require.Equal(t, key*10, v)
					return
				}
				require.ErrorIs(t, err, otter.ErrNotFound)
			},
		},
		{
			name: "error of the batch",
			fn: func(context.Context, []int) (map[int]int, error) {
				return nil, errBatch
			},
			check: func(t *testing.T, _ int, load func() (int, error)) {
				t.Helper()
				_, err := load()
				require.ErrorIs(t, err, errBatch)
			},
		},
		{
			name: "panic",
			fn: func(context.Context, []int) (map[int]int, error) {
				panic("boom")
			},
			check: func(t *testing.T, _ int, load func() (int, error)) {
				t.Helper()
				require.PanicsWithValue(t, "boom", func() { _, _ = load() })
			},
		},
		{
			name: "goexit",
			fn: func(context.Context, []int) (map[int]int, error) {
				runtime.Goexit()
				return map[int]int{}, nil
			},
			check: func(t *testing.T, _ int, load func() (int, error)) {
				t.Helper()
				_, err := load()
				require.ErrorIs(t, err, errLoaderExited)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Keys 1 and 2 share the second batch.
			release := make(chan struct{})
			tl := &testBulkLoader{fn: func(ctx context.Context, keys []int) (map[int]int, error) {
				if keys[0] == 0 {
					<-release
					return map[int]int{0: 0}, nil
				}
				return tt.fn(ctx, keys)
			}}
			l := Must[int, int](tl, nil)
			go func() {
				_, _ = l.Load(context.Background(), 0)
			}()
			waitFor(t, func() bool { return tl.runningCalls() == 1 })

			var wg sync.WaitGroup
			for _, k := range []int{1, 2} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					tt.check(t, k, func() (int, error) {
						return l.Load(context.Background(), k)
					})
				}()
			}
			waitFor(t, func() bool { return l.queued() == 2 })
			close(release)
			wg.Wait()
			require.Len(t, tl.batches, 2)
			require.ElementsMatch(t, []int{1, 2}, tl.batches[1])

			// The failed batch has freed its call.
			waitFor(t, l.idle)
			tl.fn = nil
			v, err := l.Load(context.Background(), 3)
			require.NoError(t, err)
			require.Equal(t, 30, v)
		})
	}
}

func TestLoader_Reload(t *testing.T) {
	t.Parallel()

	tl := &testBulkLoader{fn: func(_ context.Context, keys []int) (map[int]int, error) {
		if keys[0] == 1 {
			return map[int]int{}, nil
		}
		return map[int]int{keys[0]: keys[0] * 100}, nil
	}}
	l := Must[int, int](tl, nil)
	v, err := l.Reload(context.Background(), 2, 20)
	require.NoError(t, err)
	require.Equal(t, 200, v)
	_, err = l.Reload(context.Background(), 1, 10)
	require.ErrorIs(t, err, otter.ErrNotFound)
}

func TestLoader_WithCache(t *testing.T) {
	t.Parallel()

	// The misses of concurrent Gets reach the data source in two bulk loads.
	const n = 100
	tl := &testBulkLoader{release: make(chan struct{})}
	l := Must[int, int](tl, nil)
	c := otter.Must(&otter.Options[int, int]{MaximumSize: 1000})

	var wg sync.WaitGroup
	for _, k := range keysUpTo(n) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := c.Get(context.Background(), k, l)
			if err != nil || v != k*10 {
				t.Errorf("Get(%d) = %d, %v; want %d, nil", k, v, err, k*10)
			}
		}()
	}
	waitFor(t, func() bool { return tl.runningCalls() == 1 && l.queued() == n-1 })
	close(tl.release)
	wg.Wait()

	require.Equal(t, []int{1, n - 1}, tl.batchSizes())
	for _, k := range keysUpTo(n) {
		v, ok := c.GetIfPresent(k)
		require.True(t, ok)
		require.Equal(t, k*10, v)
	}
}

func TestLoader_CompactsAbandonedRequests(t *testing.T) {
	t.Parallel()

	// While a batch hangs, callers that give up do not pile up in the queue.
	tl := &testBulkLoader{release: make(chan struct{})}
	l := Must[int, int](tl, nil)
	wg := loadAll(t, l, []int{0})
	waitFor(t, func() bool { return tl.runningCalls() == 1 })

	for k := 1; k <= 1000; k++ {
		ctx, cancel := context.WithCancel(context.Background())
		res := make(chan error, 1)
		go func() {
			_, err := l.Load(ctx, k)
			res <- err
		}()
		waitFor(t, func() bool { return l.queued() == 1 })
		cancel()
		require.ErrorIs(t, <-res, context.Canceled)
	}
	l.mu.Lock()
	queueLen := len(l.loads.requests)
	l.mu.Unlock()
	require.LessOrEqual(t, queueLen, 16)

	wg2 := loadAll(t, l, []int{2000})
	waitFor(t, func() bool { return l.queued() == 1 })
	close(tl.release)
	wg.Wait()
	wg2.Wait()
	waitFor(t, l.idle)
	require.Equal(t, [][]int{{0}, {2000}}, tl.batches)
}

type testLogger struct {
	errs chan error
}

func (tl *testLogger) Warn(context.Context, string, error) {}

func (tl *testLogger) Error(_ context.Context, _ string, err error) {
	tl.errs <- err
}

func TestLoader_PanicWithoutCallers(t *testing.T) {
	t.Parallel()

	// A panic of BulkLoad that no caller waits for any more is logged with its stack instead of
	// disappearing.
	tl := &testBulkLoader{fn: func(ctx context.Context, _ []int) (map[int]int, error) {
		<-ctx.Done()
		panic("boom without callers")
	}}
	logger := &testLogger{errs: make(chan error, 1)}
	l := Must[int, int](tl, &Options{Logger: logger})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitFor(t, func() bool { return tl.runningCalls() == 1 })
		cancel()
	}()
	_, err := l.Load(ctx, 1)
	require.ErrorIs(t, err, context.Canceled)

	select {
	case err := <-logger.errs:
		var pe *xruntime.PanicError
		require.ErrorAs(t, err, &pe)
		require.Equal(t, "boom without callers", pe.Value)
		require.Contains(t, string(pe.Stack), "loader_test.go")
	case <-time.After(10 * time.Second):
		t.Fatal("the panic was not logged")
	}
	waitFor(t, l.idle)
}

func TestLoader_ReloadBatches(t *testing.T) {
	t.Parallel()

	// Reloads are batched like loads; a key reloaded twice goes once, with the old value of the
	// first call.
	tl := &testBulkLoader{release: make(chan struct{})}
	l := Must[int, int](tl, nil)
	reload := func(key, oldValue int) *sync.WaitGroup {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := l.Reload(context.Background(), key, oldValue)
			if err != nil || v != key*10 {
				t.Errorf("Reload(%d) = %d, %v; want %d, nil", key, v, err, key*10)
			}
		}()
		return &wg
	}

	wgs := []*sync.WaitGroup{reload(1, 10)}
	waitFor(t, func() bool { return tl.runningCalls() == 1 })
	wgs = append(wgs, reload(2, 20))
	waitFor(t, func() bool { return l.queued() == 1 })
	wgs = append(wgs, reload(3, 30))
	waitFor(t, func() bool { return l.queued() == 2 })
	wgs = append(wgs, reload(2, 99))
	waitFor(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		r := l.reloads.waiting[2]
		return r != nil && r.callers == 2
	})
	close(tl.release)
	for _, wg := range wgs {
		wg.Wait()
	}

	waitFor(t, l.idle)
	require.Equal(t, [][]int{{1}, {2, 3}}, tl.reloads)
	require.Equal(t, [][]int{{10}, {20, 30}}, tl.oldValues)
	require.Empty(t, tl.batches)
}

// sequence returns the batches in the order they went, as "L<key>" for loads and "R<key>" for
// reloads of one key each.
func (tl *testBulkLoader) sequence() []string {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	seq := make([]string, 0, len(tl.kinds))
	loads, reloads := 0, 0
	for _, kind := range tl.kinds {
		if kind == "load" {
			seq = append(seq, fmt.Sprintf("L%d", tl.batches[loads][0]))
			loads++
			continue
		}
		seq = append(seq, fmt.Sprintf("R%d", tl.reloads[reloads][0]))
		reloads++
	}
	return seq
}

func TestLoader_LoadsPerReload(t *testing.T) {
	t.Parallel()

	// While load 0 runs, loads 1-5 and reloads 10 and 11 queue up, one key per batch. Load 0
	// went with no reload waiting, so it does not count.
	tests := []struct {
		name           string
		loadsPerReload int
		want           []string
	}{
		{name: "turns", want: []string{"L0", "L1", "R10", "L2", "R11", "L3", "L4", "L5"}},
		{name: "three", loadsPerReload: 3, want: []string{"L0", "L1", "L2", "L3", "R10", "L4", "L5", "R11"}},
		{name: "loads first", loadsPerReload: -1, want: []string{"L0", "L1", "L2", "L3", "L4", "L5", "R10", "R11"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tl := &testBulkLoader{release: make(chan struct{})}
			l := Must[int, int](tl, &Options{MaxBatchSize: 1, LoadsPerReload: tt.loadsPerReload})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = l.Load(context.Background(), 0)
			}()
			waitFor(t, func() bool { return tl.runningCalls() == 1 })

			queue := func(key int, reload bool) {
				queued := l.queued()
				wg.Add(1)
				go func() {
					defer wg.Done()
					if reload {
						_, _ = l.Reload(context.Background(), key, key)
						return
					}
					_, _ = l.Load(context.Background(), key)
				}()
				waitFor(t, func() bool { return l.queued() == queued+1 })
			}
			for k := 1; k <= 5; k++ {
				queue(k, false)
			}
			queue(10, true)
			queue(11, true)
			close(tl.release)
			wg.Wait()

			waitFor(t, l.idle)
			require.Equal(t, tt.want, tl.sequence())
		})
	}
}

func TestLoader_WithCacheRefresh(t *testing.T) {
	t.Parallel()

	// Concurrent refreshes of a cache reach the data source in two bulk reloads.
	const n = 100
	tl := &testBulkLoader{release: make(chan struct{})}
	l := Must[int, int](tl, nil)
	c := otter.Must(&otter.Options[int, int]{
		MaximumSize:       1000,
		RefreshCalculator: otter.RefreshWriting[int, int](time.Hour),
	})
	for _, k := range keysUpTo(n) {
		c.Set(k, k)
	}

	results := make([]<-chan otter.RefreshResult[int, int], 0, n)
	for _, k := range keysUpTo(n) {
		results = append(results, c.Refresh(context.Background(), k, l))
	}
	waitFor(t, func() bool { return tl.runningCalls() == 1 && l.queued() == n-1 })
	close(tl.release)
	for _, ch := range results {
		r := <-ch
		require.NoError(t, r.Err)
		require.Equal(t, r.Key*10, r.Value)
	}

	sizes := make([]int, 0, len(tl.reloads))
	for _, keys := range tl.reloads {
		sizes = append(sizes, len(keys))
	}
	require.Equal(t, []int{1, n - 1}, sizes)
	for _, k := range keysUpTo(n) {
		v, ok := c.GetIfPresent(k)
		require.True(t, ok)
		require.Equal(t, k*10, v)
	}
}

func TestLoader_BulkLoad(t *testing.T) {
	t.Parallel()

	// The keys of one call leave in one batch, without the keys the bulk loader leaves out.
	tl := &testBulkLoader{fn: func(_ context.Context, keys []int) (map[int]int, error) {
		res := make(map[int]int)
		for _, k := range keys {
			if k != 2 {
				res[k] = k * 10
			}
		}
		return res, nil
	}}
	l := Must[int, int](tl, nil)
	res, err := l.BulkLoad(context.Background(), []int{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, map[int]int{1: 10, 3: 30}, res)
	require.Equal(t, [][]int{{1, 2, 3}}, tl.batches)
}

func TestLoader_BulkReloadOldValuesLength(t *testing.T) {
	t.Parallel()

	tl := &testBulkLoader{}
	l := Must[int, int](tl, nil)
	_, err := l.BulkReload(context.Background(), []int{1, 2}, []int{10})
	require.Error(t, err)

	// The loader still works.
	res, err := l.BulkReload(context.Background(), []int{1, 2}, []int{10, 20})
	require.NoError(t, err)
	require.Equal(t, map[int]int{1: 10, 2: 20}, res)
}

func TestLoader_BulkLoadErrors(t *testing.T) {
	t.Parallel()

	// Split into two batches that fail, BulkLoad returns both errors, each once.
	errOdd := errors.New("odd batch failed")
	errEven := errors.New("even batch failed")
	tl := &testBulkLoader{fn: func(_ context.Context, keys []int) (map[int]int, error) {
		if keys[0]%2 == 1 {
			return nil, errOdd
		}
		return nil, errEven
	}}
	l := Must[int, int](tl, &Options{MaxBatchSize: 2, MaxInFlight: -1})
	_, err := l.BulkLoad(context.Background(), []int{1, 3, 2, 4})
	require.ErrorIs(t, err, errOdd)
	require.ErrorIs(t, err, errEven)
	require.Len(t, err.(interface{ Unwrap() []error }).Unwrap(), 2)

	tl.fn = func(context.Context, []int) (map[int]int, error) {
		panic("bulk boom")
	}
	require.PanicsWithValue(t, "bulk boom", func() {
		_, _ = l.BulkLoad(context.Background(), []int{5})
	})
}

func TestLoader_BulkLoadContext(t *testing.T) {
	t.Parallel()

	// A call that gives up leaves its queued keys, which are then not loaded. Key 5 is asked for
	// twice, so the call leaves it twice.
	tl := &testBulkLoader{release: make(chan struct{})}
	l := Must[int, int](tl, nil)
	wg := loadAll(t, l, []int{0})
	waitFor(t, func() bool { return tl.runningCalls() == 1 })

	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		_, err := l.BulkLoad(ctx, []int{5, 5, 6})
		res <- err
	}()
	waitFor(t, func() bool { return l.queued() == 2 })
	cancel()
	require.ErrorIs(t, <-res, context.Canceled)
	require.Zero(t, l.queued())

	close(tl.release)
	wg.Wait()
	waitFor(t, l.idle)
	require.Equal(t, [][]int{{0}}, tl.batches)
}

func TestLoader_WithCacheBulkGet(t *testing.T) {
	t.Parallel()

	// The keys of a BulkGet and of a Get share a batch.
	tl := &testBulkLoader{release: make(chan struct{})}
	l := Must[int, int](tl, nil)
	c := otter.Must(&otter.Options[int, int]{MaximumSize: 1000})
	go func() {
		_, _ = c.Get(context.Background(), 0, l)
	}()
	waitFor(t, func() bool { return tl.runningCalls() == 1 })

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		res, err := c.BulkGet(context.Background(), []int{1, 2, 3}, l)
		if err != nil || len(res) != 3 || res[1] != 10 || res[2] != 20 || res[3] != 30 {
			t.Errorf("BulkGet = %v, %v", res, err)
		}
	}()
	waitFor(t, func() bool { return l.queued() == 3 })
	go func() {
		defer wg.Done()
		v, err := c.Get(context.Background(), 4, l)
		if err != nil || v != 40 {
			t.Errorf("Get(4) = %d, %v", v, err)
		}
	}()
	waitFor(t, func() bool { return l.queued() == 4 })
	close(tl.release)
	wg.Wait()

	waitFor(t, l.idle)
	require.Len(t, tl.batches, 2)
	require.ElementsMatch(t, []int{1, 2, 3, 4}, tl.batches[1])
}

func TestLoader_WithCacheBulkRefresh(t *testing.T) {
	t.Parallel()

	// The keys of a BulkRefresh and of a Refresh share a batch of reloads, with their old values.
	tl := &testBulkLoader{release: make(chan struct{})}
	l := Must[int, int](tl, nil)
	c := otter.Must(&otter.Options[int, int]{
		MaximumSize:       1000,
		RefreshCalculator: otter.RefreshWriting[int, int](time.Hour),
	})
	for _, k := range keysUpTo(5) {
		c.Set(k, k)
	}

	first := c.Refresh(context.Background(), 0, l)
	waitFor(t, func() bool { return tl.runningCalls() == 1 })
	bulk := c.BulkRefresh(context.Background(), []int{1, 2, 3}, l)
	waitFor(t, func() bool { return l.queued() == 3 })
	single := c.Refresh(context.Background(), 4, l)
	waitFor(t, func() bool { return l.queued() == 4 })
	close(tl.release)

	require.NoError(t, (<-first).Err)
	for _, r := range <-bulk {
		require.NoError(t, r.Err)
		require.Equal(t, r.Key*10, r.Value)
	}
	require.NoError(t, (<-single).Err)

	waitFor(t, l.idle)
	require.Len(t, tl.reloads, 2)
	require.ElementsMatch(t, []int{1, 2, 3, 4}, tl.reloads[1])
	// Each old value is the key's own value.
	require.ElementsMatch(t, tl.reloads[1], tl.oldValues[1])
	for i, k := range tl.reloads[1] {
		require.Equal(t, k, tl.oldValues[1][i])
	}
}

// fakeTimers records the functions a Loader schedules with afterFunc, for the test to fire. Each
// one gets a timer that never fires on its own, so the test can see whether the Loader stopped it.
type fakeTimers struct {
	mu     sync.Mutex
	fns    []func()
	timers []*time.Timer
}

func (ft *fakeTimers) afterFunc(_ time.Duration, f func()) *time.Timer {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.fns = append(ft.fns, f)
	t := time.NewTimer(time.Hour)
	ft.timers = append(ft.timers, t)
	return t
}

// stopped reports whether the Loader stopped the i-th timer.
func (ft *fakeTimers) stopped(i int) bool {
	ft.mu.Lock()
	t := ft.timers[i]
	ft.mu.Unlock()
	return !t.Stop()
}

func (ft *fakeTimers) count() int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return len(ft.fns)
}

func (ft *fakeTimers) fire(i int) {
	ft.mu.Lock()
	f := ft.fns[i]
	ft.mu.Unlock()
	f()
}

// newDelayed returns a Loader whose waits for more keys are fired by the test.
func newDelayed(tl *testBulkLoader, o Options) (*Loader[int, int], *fakeTimers) {
	o.MaxDelay = time.Minute
	l := Must[int, int](tl, &o)
	ft := &fakeTimers{}
	l.afterFunc = ft.afterFunc
	return l, ft
}

func TestLoader_DelayCollectsKeys(t *testing.T) {
	t.Parallel()

	// With no limit on calls, the keys that come during the wait leave together.
	tl := &testBulkLoader{}
	l, ft := newDelayed(tl, Options{MaxInFlight: -1})
	wg := loadAll(t, l, []int{1, 2, 3})
	waitFor(t, func() bool { return l.queued() == 3 })
	require.Equal(t, 1, ft.count())
	require.Zero(t, tl.runningCalls())

	ft.fire(0)
	wg.Wait()
	waitFor(t, l.idle)
	require.Len(t, tl.batches, 1)
	require.ElementsMatch(t, []int{1, 2, 3}, tl.batches[0])
}

func TestLoader_DelayEndsWhenBatchIsFull(t *testing.T) {
	t.Parallel()

	// A full batch leaves before the wait is over; the next key starts a wait of its own.
	tl := &testBulkLoader{}
	l, ft := newDelayed(tl, Options{MaxInFlight: -1, MaxBatchSize: 2})
	wg := loadAll(t, l, []int{1, 2})
	wg.Wait()
	require.Len(t, tl.batches, 1)
	require.ElementsMatch(t, []int{1, 2}, tl.batches[0])
	// The wait cut short by the full batch has its timer stopped.
	require.True(t, ft.stopped(0))

	wg = loadAll(t, l, []int{3})
	waitFor(t, func() bool { return l.queued() == 1 && ft.count() == 2 })
	// The wait of the first batch is over already and does not send key 3.
	ft.fire(0)
	require.Equal(t, 1, l.queued())
	ft.fire(1)
	wg.Wait()
	require.Equal(t, [][]int{{3}}, tl.batches[1:])
}

func TestLoader_DelayOverWhileCallsAreBusy(t *testing.T) {
	t.Parallel()

	// Keys whose wait is over leave as soon as a call is free.
	tl := &testBulkLoader{release: make(chan struct{})}
	l, ft := newDelayed(tl, Options{})
	wg := loadAll(t, l, []int{0})
	waitFor(t, func() bool { return ft.count() == 1 })
	ft.fire(0)
	waitFor(t, func() bool { return tl.runningCalls() == 1 })

	wg2 := loadAll(t, l, []int{1, 2})
	waitFor(t, func() bool { return l.queued() == 2 && ft.count() == 2 })
	ft.fire(1)
	require.Equal(t, 1, tl.runningCalls())
	require.Equal(t, 2, l.queued())

	close(tl.release)
	wg.Wait()
	wg2.Wait()
	waitFor(t, l.idle)
	require.Len(t, tl.batches, 2)
	require.ElementsMatch(t, []int{1, 2}, tl.batches[1])
}

func TestLoader_DelayOfAbandonedKeys(t *testing.T) {
	t.Parallel()

	// When every queued key is abandoned, its wait is dropped: a later key waits on its own.
	tl := &testBulkLoader{}
	l, ft := newDelayed(tl, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		_, err := l.Load(ctx, 1)
		res <- err
	}()
	waitFor(t, func() bool { return l.queued() == 1 })
	cancel()
	require.ErrorIs(t, <-res, context.Canceled)

	wg := loadAll(t, l, []int{2})
	waitFor(t, func() bool { return l.queued() == 1 && ft.count() == 2 })
	ft.fire(0)
	require.Equal(t, 1, l.queued())
	ft.fire(1)
	wg.Wait()
	require.Equal(t, [][]int{{2}}, tl.batches)
}

func TestLoader_DelayWithRealTimer(t *testing.T) {
	t.Parallel()

	tl := &testBulkLoader{}
	l := Must[int, int](tl, &Options{MaxDelay: time.Millisecond})
	v, err := l.Load(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 10, v)
}
