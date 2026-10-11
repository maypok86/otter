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
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

// testBulkLoader records the batches it loads. Every call waits until release is closed, if it
// is set, and then runs fn, which by default returns key*10 for every key.
type testBulkLoader struct {
	release chan struct{}
	fn      func(ctx context.Context, keys []int) (map[int]int, error)

	mu         sync.Mutex
	batches    [][]int
	running    int
	maxRunning int
}

func (tl *testBulkLoader) BulkLoad(ctx context.Context, keys []int) (map[int]int, error) {
	tl.mu.Lock()
	tl.batches = append(tl.batches, slices.Clone(keys))
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

func (tl *testBulkLoader) BulkReload(ctx context.Context, keys, _ []int) (map[int]int, error) {
	return tl.BulkLoad(ctx, keys)
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
	return len(l.waiting)
}

func (l *Loader[K, V]) idle() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inFlight == 0 && len(l.waiting) == 0 && len(l.queue) == 0
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
	require.Panics(t, func() {
		Must[int, int](nil, nil)
	})

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
		r := l.waiting[7]
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
	queueLen := len(l.queue)
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
