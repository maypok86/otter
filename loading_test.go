// Copyright (c) 2024 Alexey Mayshev and contributors. All rights reserved.
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
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/stats"
)

type errValue struct{}

func (err *errValue) Error() string {
	return "error value"
}

type testLogger struct {
	calls atomic.Uint64
	warns atomic.Uint64
	errs  atomic.Uint64
}

func newTestLogger() *testLogger {
	return &testLogger{}
}

func (l *testLogger) Warn(ctx context.Context, msg string, err error) {
	l.calls.Add(1)
	l.warns.Add(1)
}

func (l *testLogger) Error(ctx context.Context, msg string, err error) {
	l.calls.Add(1)
	l.errs.Add(1)
}

type testLoader[K comparable, V any] struct {
	fn      func(ctx context.Context, key K) (V, error)
	calls   atomic.Uint64
	loads   atomic.Uint64
	reloads atomic.Uint64
}

func newTestLoader[K comparable, V any](fn func(ctx context.Context, key K) (V, error)) *testLoader[K, V] {
	return &testLoader[K, V]{fn: fn}
}

func (tl *testLoader[K, V]) Load(ctx context.Context, key K) (V, error) {
	tl.calls.Add(1)
	tl.loads.Add(1)
	return tl.fn(ctx, key)
}

func (tl *testLoader[K, V]) Reload(ctx context.Context, key K, oldValue V) (V, error) {
	tl.calls.Add(1)
	tl.reloads.Add(1)
	return tl.fn(ctx, key)
}

type testBulkLoader[K comparable, V any] struct {
	fn      func(ctx context.Context, keys []K) (map[K]V, error)
	calls   atomic.Uint64
	loads   atomic.Uint64
	reloads atomic.Uint64
}

func newTestBulkLoader[K comparable, V any](fn func(ctx context.Context, keys []K) (map[K]V, error)) *testBulkLoader[K, V] {
	return &testBulkLoader[K, V]{fn: fn}
}

func (tl *testBulkLoader[K, V]) BulkLoad(ctx context.Context, keys []K) (map[K]V, error) {
	tl.calls.Add(1)
	tl.loads.Add(1)
	return tl.fn(ctx, keys)
}

func (tl *testBulkLoader[K, V]) BulkReload(ctx context.Context, keys []K, oldValues []V) (map[K]V, error) {
	tl.calls.Add(1)
	tl.reloads.Add(1)
	return tl.fn(ctx, keys)
}

func TestCache_GetPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		panicValue       any
		wrappedErrorType bool
	}{
		{
			name:             "panicError wraps non-error type",
			panicValue:       &panicError{value: "string value"},
			wrappedErrorType: false,
		},
		{
			name:             "panicError wraps error type",
			panicValue:       &panicError{value: new(errValue)},
			wrappedErrorType: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
				panic(tt.panicValue)
			})

			ctx := context.Background()
			size := 100
			statsCounter := stats.NewCounter()
			c := Must(&Options[int, int]{
				MaximumSize:      size,
				StatsRecorder:    statsCounter,
				ExpiryCalculator: ExpiryWriting[int, int](5 * time.Minute),
			})

			k1 := 1
			var recovered any

			func() {
				defer func() {
					recovered = recover()
				}()

				_, _ = c.Get(ctx, k1, tl)
			}()

			if recovered == nil {
				t.Fatal("expected a non-nil panic value")
			}

			err, ok := recovered.(error)
			if !ok {
				t.Fatalf("recovered non-error type: %T", recovered)
			}

			if !errors.Is(err, new(errValue)) && tt.wrappedErrorType {
				t.Fatalf("unexpected wrapped error type %T; want %T", err, new(errValue))
			}

			if c.cache.singleflight.getCall(k1) != nil {
				t.Fatal("the call should be deleted even in case of panic")
			}
			if c.EstimatedSize() > 0 {
				t.Fatal("the cache should be empty after panic")
			}
		})
	}
}

func TestCache_BulkGetPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		panicValue       any
		wrappedErrorType bool
	}{
		{
			name:             "panicError wraps non-error type",
			panicValue:       &panicError{value: "string value"},
			wrappedErrorType: false,
		},
		{
			name:             "panicError wraps error type",
			panicValue:       &panicError{value: new(errValue)},
			wrappedErrorType: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
				panic(tt.panicValue)
			})

			ctx := context.Background()
			size := 100
			statsCounter := stats.NewCounter()
			c := Must(&Options[int, int]{
				MaximumSize:      size,
				ExpiryCalculator: ExpiryWriting[int, int](5 * time.Minute),
				StatsRecorder:    statsCounter,
			})

			ks := []int{1, 2}
			var recovered any

			func() {
				defer func() {
					recovered = recover()
				}()

				_, _ = c.BulkGet(ctx, ks, tl)
			}()

			if recovered == nil {
				t.Fatal("expected a non-nil panic value")
			}

			err, ok := recovered.(error)
			if !ok {
				t.Fatalf("recovered non-error type: %T", recovered)
			}

			if !errors.Is(err, new(errValue)) && tt.wrappedErrorType {
				t.Fatalf("unexpected wrapped error type %T; want %T", err, new(errValue))
			}

			for _, k := range ks {
				if c.cache.singleflight.getCall(k) != nil {
					t.Fatal("calls should be deleted even in case of panic")
				}
			}
			if c.EstimatedSize() > 0 {
				t.Fatal("the cache should be empty after panic")
			}
		})
	}
}

func TestCache_GetWithSuccessLoad(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: ExpiryWriting[int, int](5 * time.Minute),
	})

	k1 := 1
	v1 := 100
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		return v1, nil
	})

	v, err := c.Get(ctx, k1, tl)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if v != v1 {
		t.Fatalf("Get value = %v; want = %v", v, v1)
	}

	v, err = c.Get(ctx, k1, tl)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if v != v1 {
		t.Fatalf("Get value = %v; want = %v", v, v1)
	}

	if e, ok := c.GetEntryQuietly(k1); c.EstimatedSize() != 1 || ok && e.Value != v1 {
		t.Fatalf("the cache should only contain the key = %v", k1)
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 1 ||
		snapshot.Misses != 1 ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_GetCoalescesConcurrentLoads(t *testing.T) {
	t.Parallel()

	// A thundering herd of first-time Gets for the same missing key must
	// collapse to exactly one load. otter coalesces callers that overlap an
	// in-flight load; the hazard is the load-completion boundary, where a
	// caller can win a just-freed single-flight slot, miss the value that has
	// not yet been published, and start a redundant second load. group.finish
	// publishes the value before freeing the slot, and Get re-checks the map
	// after winning the slot, so winning implies the value is visible and no
	// second load occurs.
	ctx := context.Background()
	c := Must(&Options[int, int]{MaximumSize: 1000})

	const (
		concurrency = 64
		rounds      = 50
	)
	for round := range rounds {
		key := round // a fresh missing key each round, so every Get is a load
		tl := newTestLoader[int, int](func(_ context.Context, k int) (int, error) {
			return k * 10, nil
		})
		var wg sync.WaitGroup
		wg.Add(concurrency)
		for range concurrency {
			go func() {
				defer wg.Done()
				v, err := c.Get(ctx, key, tl)
				require.NoError(t, err)
				require.Equal(t, key*10, v)
			}()
		}
		wg.Wait()
		require.Equalf(t, uint64(1), tl.loads.Load(),
			"round %d: concurrent first-time loads for the same key must coalesce to one", round)
	}
}

func TestCache_GetDoesNotOverwriteConcurrentSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := Must(&Options[int, int]{})
	for key := range 10_000 {
		setDone := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.Set(key, 1)
			close(setDone)
		}()
		go func() {
			defer wg.Done()
			_, _ = c.Get(ctx, key, LoaderFunc[int, int](func(context.Context, int) (int, error) {
				<-setDone
				return 2, nil
			}))
		}()
		wg.Wait()
		v, _ := c.GetIfPresent(key)
		require.Equalf(t, 1, v, "key %d: a load overwrote a Set that returned before the load did", key)
	}
}

func TestCache_GetWithNotFoundLoad(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: ExpiryWriting[int, int](5 * time.Minute),
	})

	k1 := 1
	v1 := 100
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		return v1, nil
	})

	v, err := c.Get(ctx, k1, tl)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if v != v1 {
		t.Fatalf("Get value = %v; want = %v", v, v1)
	}

	v, err = c.Get(ctx, k1, tl)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if v != v1 {
		t.Fatalf("Get value = %v; want = %v", v, v1)
	}

	someErr := fmt.Errorf("olololo: %w", ErrNotFound)
	tl1 := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		return 0, someErr
	})

	c.Invalidate(k1)
	v, err = c.Get(ctx, k1, tl1)
	require.Equal(t, someErr, err)
	require.Zero(t, v)

	if _, ok := c.GetEntryQuietly(k1); c.EstimatedSize() != 0 || ok {
		t.Fatalf("the cache should only contain the key = %v", k1)
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 1 ||
		snapshot.Misses != 2 ||
		snapshot.Loads() != 2 ||
		snapshot.LoadSuccesses != 2 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_GetWithSuccessRefresh(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	size := 100
	statsCounter := stats.NewCounter()
	fs := &fakeSource{}
	c := Must(&Options[int, int]{
		MaximumSize:   size,
		StatsRecorder: statsCounter,
		Clock:         fs,
		Executor: func(fn func()) {
			fn()
		},
		RefreshCalculator: RefreshWriting[int, int](10 * time.Minute),
	})

	k1 := 1
	v1 := 100
	v2 := 101
	c.Set(k1, v1)

	fs.Sleep(10*time.Minute + time.Second)
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		if key == k1 {
			return v2, ctx.Err()
		}
		panic("not valid key")
	})

	cancel()
	v, err := c.Get(ctx, k1, tl)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if v != v1 {
		t.Fatalf("Get value = %v; want = %v", v, v1)
	}

	v, ok := c.GetIfPresent(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if v != v2 {
		t.Fatalf("GetIfPresent value = %v; want = %v", v, v2)
	}

	c.Set(k1, v1)
	v, err = c.Get(ctx, k1, tl)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if v != v1 {
		t.Fatalf("Get value = %v; want = %v", v, v1)
	}

	if tl.reloads.Load() != 1 && tl.loads.Load() != 0 {
		t.Fatalf("not valid loader stats. loads = %v, reloads = %v", tl.loads.Load(), tl.reloads.Load())
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 3 ||
		snapshot.Misses != 0 ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_GetWithNotFoundRefresh(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	fs := &fakeSource{}
	c := Must(&Options[int, int]{
		MaximumSize:   size,
		StatsRecorder: statsCounter,
		Clock:         fs,
		Executor: func(fn func()) {
			fn()
		},
		RefreshCalculator: RefreshWriting[int, int](time.Hour + time.Minute),
	})

	k1 := 1
	v1 := 100
	c.Set(k1, v1)

	fs.Sleep(time.Hour + time.Minute + time.Second)
	someErr := fmt.Errorf("olololo: %w", ErrNotFound)
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		if key == k1 {
			return 0, someErr
		}
		panic("not valid key")
	})

	v, err := c.Get(ctx, k1, tl)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if v != v1 {
		t.Fatalf("Get value = %v; want = %v", v, v1)
	}

	v, ok := c.GetIfPresent(k1)
	require.False(t, ok)
	require.Zero(t, v)

	if tl.reloads.Load() != 1 && tl.loads.Load() != 0 {
		t.Fatalf("not valid loader stats. loads = %v, reloads = %v", tl.loads.Load(), tl.reloads.Load())
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 1 ||
		snapshot.Misses != 1 ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_Refresh(t *testing.T) {
	t.Parallel()

	size := 100
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:       size,
		StatsRecorder:     statsCounter,
		RefreshCalculator: RefreshWriting[int, int](time.Second),
	})

	k1 := 1
	v1 := 100
	v2 := 101
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	tl1 := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		<-done
		if key == k1 {
			return v1, ctx.Err()
		}
		panic("not valid key")
	})
	tl2 := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		if key == k1 {
			return v2, ctx.Err()
		}
		panic("not valid key")
	})

	ch := c.Refresh(ctx, k1, tl1)
	cancel()

	v, ok := c.GetIfPresent(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	if v != 0 {
		t.Fatalf("GetIfPresent value = %v; want = %v", v, 0)
	}
	done <- struct{}{}

	<-ch

	v, ok = c.GetIfPresent(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if v != v1 {
		t.Fatalf("GetIfPresent value = %v; want = %v", v, v1)
	}

	<-c.Refresh(ctx, k1, tl2)

	v, ok = c.GetIfPresent(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if v != v2 {
		t.Fatalf("GetIfPresent value = %v; want = %v", v, v2)
	}

	if tl1.reloads.Load() != 0 && tl1.loads.Load() != 1 {
		t.Fatalf("not valid loader stats. loads = %v, reloads = %v", tl1.loads.Load(), tl1.reloads.Load())
	}
	if tl2.reloads.Load() != 1 && tl2.loads.Load() != 0 {
		t.Fatalf("not valid loader stats. loads = %v, reloads = %v", tl2.loads.Load(), tl2.reloads.Load())
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 2 ||
		snapshot.Misses != 1 ||
		snapshot.Loads() != 2 ||
		snapshot.LoadSuccesses != 2 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_BulkGetWithSuccessLoad(t *testing.T) {
	t.Parallel()

	size := 100

	keys := []int{0, 1, 1, 2, 3, 5, 6, 7, 8, 3, 9}
	toSet := []int{3, 7, 0}
	tl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		m := make(map[int]int, len(keys))
		for _, k := range keys {
			m[k] = k + 100
		}
		return m, nil
	})

	ctx := context.Background()
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: ExpiryWriting[int, int](5 * time.Minute),
	})

	for _, k := range toSet {
		c.Set(k, k)
	}
	ks := make(map[int]bool, len(toSet))
	for _, k := range toSet {
		ks[k] = true
	}

	res, err := c.BulkGet(ctx, keys, tl)
	if err != nil {
		t.Fatalf("BulkGet error = %v", err)
	}

	for k, v := range res {
		if ks[k] {
			if v != k {
				t.Fatalf("value should be equal to key. key: %v, value: %v", k, v)
			}
			continue
		}
		if v != k+100 {
			t.Fatalf("value should be equal to key+100. key: %v, value: %v", k, v)
		}
	}

	if c.EstimatedSize() != 9 {
		t.Fatalf("the cache should only contain unique keys")
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 3 ||
		snapshot.Misses != 6 ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}

	res, err = c.BulkGet(ctx, []int{0}, tl)
	if err != nil {
		t.Fatalf("BulkGet error = %v", err)
	}
	if res[0] != 0 {
		t.Fatalf("value should be equal to key. key: %v, value: %v", 0, 0)
	}

	snapshot = statsCounter.Snapshot()
	if snapshot.Hits != 4 ||
		snapshot.Misses != 6 ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_BulkGetCoalescesConcurrentLoads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := Must(&Options[int, int]{MaximumSize: 1000})

	const (
		concurrency = 64
		rounds      = 50
	)
	for round := range rounds {
		key := round
		tbl := newTestBulkLoader[int, int](func(_ context.Context, keys []int) (map[int]int, error) {
			return map[int]int{key: key * 10}, nil
		})
		var wg sync.WaitGroup
		wg.Add(concurrency)
		for range concurrency {
			go func() {
				defer wg.Done()
				res, err := c.BulkGet(ctx, []int{key}, tbl)
				require.NoError(t, err)
				require.Equal(t, key*10, res[key])
			}()
		}
		wg.Wait()
		require.Equalf(t, uint64(1), tbl.loads.Load(),
			"round %d: concurrent first-time bulk loads for the same key must coalesce to one", round)
	}
}

func TestCache_BulkGetWithSuccessRefresh(t *testing.T) {
	t.Parallel()

	size := 100

	keys := []int{0, 1, 1, 2, 3, 5, 6, 7, 8, 3, 9}
	toUpdate := []int{3, 7, 0}

	ctx, cancel := context.WithCancel(context.Background())
	statsCounter := stats.NewCounter()
	fs := &fakeSource{}
	c := Must(&Options[int, int]{
		MaximumSize:   size,
		StatsRecorder: statsCounter,
		Clock:         fs,
		Executor: func(fn func()) {
			fn()
		},
		RefreshCalculator: RefreshWriting[int, int](10 * time.Minute),
	})

	for _, k := range keys {
		c.Set(k, k)
	}
	ks := make(map[int]bool, len(toUpdate))
	for _, k := range toUpdate {
		ks[k] = true
	}

	fs.Sleep(10*time.Minute + time.Nanosecond)
	for _, k := range toUpdate {
		c.Set(k, k)
	}

	var calls atomic.Int64
	tl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		calls.Add(1)

		m := make(map[int]int, len(keys))
		for _, k := range keys {
			if calls.Load() == 1 && ks[k] {
				t.Fatalf("key should not be loaded. key: %v", k)
			}
			m[k] = k + 100
		}
		return m, ctx.Err()
	})

	cancel()
	res, err := c.BulkGet(ctx, keys, tl)
	if err != nil {
		t.Fatalf("BulkGet error = %v", err)
	}

	for k, v := range res {
		if v != k {
			t.Fatalf("value should be equal to key. key: %v, value: %v", k, v)
		}
	}
	for _, k := range keys {
		v, ok := c.GetIfPresent(k)
		if !ok {
			t.Fatalf("not found key = %v", k)
		}
		if ks[k] {
			if v != k {
				t.Fatalf("value should be equal to key. key: %v, value: %v", k, v)
			}
			continue
		}
		if v != k+100 {
			t.Fatalf("value should be equal to key+100. key: %v, value: %v", k, v)
		}
	}

	if c.EstimatedSize() != 9 {
		t.Fatalf("the cache should only contain unique keys")
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 20 ||
		snapshot.Misses != 0 ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_BulkGetWithNotFoundRefresh(t *testing.T) {
	t.Parallel()

	size := 100

	notFound := 3
	keys := append([]int{0, 1, 2}, notFound)

	ctx := context.Background()
	statsCounter := stats.NewCounter()
	var (
		mutex sync.Mutex
		wg    sync.WaitGroup
	)
	m := make(map[DeletionCause]int)
	fs := &fakeSource{}
	wg.Add(len(keys))
	c := Must(&Options[int, int]{
		MaximumSize:   size,
		StatsRecorder: statsCounter,
		Clock:         fs,
		Executor: func(fn func()) {
			fn()
		},
		RefreshCalculator: RefreshWriting[int, int](10 * time.Hour),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
			wg.Done()
		},
	})

	for _, k := range keys {
		c.Set(k, k)
	}

	fs.Sleep(10*time.Hour + time.Millisecond)
	tl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		m := make(map[int]int, len(keys))
		for _, k := range keys {
			if k == notFound {
				continue
			}
			m[k] = k + 100
		}
		return m, nil
	})

	res, err := c.BulkGet(ctx, keys, tl)
	if err != nil {
		t.Fatalf("BulkGet error = %v", err)
	}

	for k, v := range res {
		if v != k {
			t.Fatalf("value should be equal to key. key: %v, value: %v", k, v)
		}
	}
	for _, k := range keys {
		v, ok := c.GetIfPresent(k)
		if k == notFound {
			require.False(t, ok)
			require.Zero(t, v)
		} else {
			require.True(t, ok)
			require.Equal(t, k+100, v)
		}
	}

	if c.EstimatedSize() != len(keys)-1 {
		t.Fatalf("the cache should only contain unique keys")
	}

	c.CleanUp()

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != uint64(2*len(keys)-1) ||
		snapshot.Misses != 1 ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}

	wg.Wait()
	mutex.Lock()
	defer mutex.Unlock()

	require.Len(t, m, 2)
	require.Equal(t, m[CauseInvalidation], 1)
	require.Equal(t, m[CauseReplacement], 3)
}

func TestCache_BulkGetWithFakeCall(t *testing.T) {
	t.Parallel()

	size := 100

	fake := 3
	keys := []int{0, 1, 2}

	ctx := context.Background()
	statsCounter := stats.NewCounter()
	var (
		mutex sync.Mutex
		wg    sync.WaitGroup
	)
	m := make(map[DeletionCause]int)
	fs := &fakeSource{}
	wg.Add(len(keys))
	c := Must(&Options[int, int]{
		MaximumSize:   size,
		StatsRecorder: statsCounter,
		Clock:         fs,
		Executor: func(fn func()) {
			fn()
		},
		RefreshCalculator: RefreshWriting[int, int](time.Minute),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
			wg.Done()
		},
	})

	for _, k := range keys {
		c.Set(k, k)
	}

	v, ok := c.GetIfPresent(fake)
	require.False(t, ok)
	require.Zero(t, v)

	fs.Sleep(time.Minute + time.Microsecond)
	tl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		m := make(map[int]int, len(keys))
		for _, k := range keys {
			m[k] = k + 100
		}
		m[fake] = fake + 100
		return m, nil
	})

	res, err := c.BulkGet(ctx, keys, tl)
	if err != nil {
		t.Fatalf("BulkGet error = %v", err)
	}

	for k, v := range res {
		require.NotEqual(t, fake, k)
		if v != k {
			t.Fatalf("value should be equal to key. key: %v, value: %v", k, v)
		}
	}
	for _, k := range keys {
		v, ok := c.GetIfPresent(k)
		require.True(t, ok)
		require.Equal(t, k+100, v)
	}
	v, ok = c.GetIfPresent(fake)
	require.True(t, ok)
	require.Equal(t, fake+100, v)

	if c.EstimatedSize() != len(keys)+1 {
		t.Fatalf("the cache should only contain unique keys")
	}

	c.CleanUp()

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != uint64(2*len(keys)+1) ||
		snapshot.Misses != 1 ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}

	wg.Wait()
	mutex.Lock()
	defer mutex.Unlock()

	require.Len(t, m, 1)
	require.Equal(t, m[CauseReplacement], len(keys))
}

func TestCache_BulkRefresh(t *testing.T) {
	t.Parallel()

	size := 100

	keys := []int{0, 1, 1, 2, 3, 5, 6, 7, 8, 3, 9}
	toUpdate := []int{3, 7, 0}
	ctx, cancel := context.WithCancel(context.Background())

	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:       size,
		StatsRecorder:     statsCounter,
		RefreshCalculator: RefreshWriting[int, int](time.Second),
	})

	ks := make(map[int]bool, len(toUpdate))
	for _, k := range toUpdate {
		ks[k] = true
	}

	for _, k := range toUpdate {
		c.Set(k, k+1)
	}

	var calls atomic.Int64
	done := make(chan struct{})
	tl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		calls.Add(1)
		if calls.Load() == 1 {
			<-done
		}

		m := make(map[int]int, len(keys))
		for _, k := range keys {
			if calls.Load() == 1 && ks[k] {
				t.Fatalf("key should not be loaded. key: %v", k)
			}
			m[k] = k + 100
		}
		return m, ctx.Err()
	})

	cancel()
	ch := c.BulkRefresh(ctx, keys, tl)

	for _, k := range keys {
		v, ok := c.GetIfPresent(k)
		if ks[k] {
			if !ok {
				t.Fatalf("not found key = %v", k)
			}
			if v != k+1 {
				t.Fatalf("value should be equal to key+1. key: %v, value: %v", k, v)
			}
			continue
		}
		if ok {
			t.Fatalf("found key = %v", k)
		}
		if v != 0 {
			t.Fatalf("value should be equal to 0. key: %v, value: %v", k, v)
		}
	}
	done <- struct{}{}

	<-ch

	for _, k := range keys {
		v, ok := c.GetIfPresent(k)
		if !ok {
			t.Fatalf("not found key = %v", k)
		}
		if v != k+100 {
			t.Fatalf("value should be equal to key+100. key: %v, value: %v", k, v)
		}
	}

	if c.EstimatedSize() != 9 {
		t.Fatalf("the cache should only contain unique keys")
	}

	if tl.reloads.Load() != 1 && tl.loads.Load() != 1 {
		t.Fatalf("not valid loader stats. loads = %v, reloads = %v", tl.loads.Load(), tl.reloads.Load())
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 15 ||
		snapshot.Misses != 7 ||
		snapshot.Loads() != 2 ||
		snapshot.LoadSuccesses != 2 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_BulkRefreshResults(t *testing.T) {
	t.Parallel()

	size := 100

	keys := []int{0, 1, 2}
	keySet := make(map[int]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	toLoad := 0

	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:   size,
		StatsRecorder: statsCounter,
		Executor: func(fn func()) {
			fn()
		},
		RefreshCalculator: RefreshWriting[int, int](time.Hour),
	})

	for _, k := range keys {
		if k == toLoad {
			continue
		}
		c.Set(k, k)
	}
	c.CleanUp()

	// The Get starts loading toLoad first, so BulkRefresh finds its call and waits for it. The
	// bulk reload of the other keys lets the Get's load finish.
	ctx := context.Background()
	loadStarted := make(chan struct{})
	waitLoad := make(chan struct{})
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		if key == toLoad {
			close(loadStarted)
			<-waitLoad
			return key + 101, nil
		}
		panic("not valid key")
	})
	btl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		m := make(map[int]int, len(keys))
		for _, k := range keys {
			m[k] = k + 100
		}
		waitLoad <- struct{}{}
		return m, nil
	})

	getDone := make(chan struct{})
	go func() {
		defer close(getDone)
		v, err := c.Get(ctx, toLoad, tl)
		require.NoError(t, err)
		require.Equal(t, toLoad+101, v)
	}()
	<-loadStarted

	results := <-c.BulkRefresh(ctx, keys, btl)
	<-getDone

	require.Equal(t, len(keys), len(results))
	for _, r := range results {
		require.True(t, keySet[r.Key])
		require.NoError(t, r.Err)
		if r.Key == toLoad {
			require.Equal(t, r.Key+101, r.Value)
		} else {
			require.Equal(t, r.Key+100, r.Value)
		}
	}

	for _, k := range keys {
		v, ok := c.GetIfPresent(k)
		require.True(t, ok)
		if k == toLoad {
			require.Equal(t, k+101, v)
		} else {
			require.Equal(t, k+100, v)
		}
	}

	if c.EstimatedSize() != 3 {
		t.Fatalf("the cache should only contain unique keys")
	}

	if tl.reloads.Load() != 1 && tl.loads.Load() != 1 {
		t.Fatalf("not valid loader stats. loads = %v, reloads = %v", tl.loads.Load(), tl.reloads.Load())
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 3 ||
		snapshot.Misses != 1 ||
		snapshot.Loads() != 2 ||
		snapshot.LoadSuccesses != 2 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_GetWithFailedLoad(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: ExpiryWriting[int, int](5 * time.Minute),
	})

	k1 := 1
	someErr := errors.New("some error")
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		return 0, someErr
	})

	v, err := c.Get(ctx, k1, tl)
	if err != someErr {
		t.Fatalf("Get error = %v; want someErr %v", err, someErr)
	}
	if v != 0 {
		t.Fatalf("unexpected non-zero value %#v", v)
	}

	v, err = c.Get(ctx, k1, newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		return 0, ErrNotFound
	}))
	if err != ErrNotFound {
		t.Fatalf("Get error = %v; want ErrNotFound", err)
	}
	if v != 0 {
		t.Fatalf("unexpected non-zero value %#v", v)
	}

	if c.EstimatedSize() > 0 {
		t.Fatal("the cache should be empty")
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits > 0 ||
		snapshot.Misses != 2 ||
		snapshot.Loads() != 2 ||
		snapshot.LoadSuccesses != 1 ||
		snapshot.LoadFailures != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_GetWithFailedRefresh(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	l := newTestLogger()
	fs := &fakeSource{}
	c := Must(&Options[int, int]{
		MaximumSize:   size,
		StatsRecorder: statsCounter,
		Clock:         fs,
		Executor: func(fn func()) {
			fn()
		},
		RefreshCalculator: RefreshCreating[int, int](5 * time.Second),
		Logger:            l,
	})

	k1 := 1
	someErr := errors.New("some error")
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		return 0, someErr
	})

	c.Set(k1, 0)
	fs.Sleep(6 * time.Second)
	v, err := c.Get(ctx, k1, tl)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if v != 0 {
		t.Fatalf("Get value = %v; want = %v", v, 0)
	}

	if l.calls.Load() != 1 && l.errs.Load() != 1 {
		t.Fatalf("not valid logger stats. errs = %v, warns = %v", l.errs.Load(), l.warns.Load())
	}

	v, err = c.Get(ctx, k1, newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		return 0, ErrNotFound
	}))
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if v != 0 {
		t.Fatalf("Get value = %v; want = %v", v, 0)
	}

	if l.calls.Load() != 1 && l.errs.Load() != 1 {
		t.Fatalf("not valid logger stats. errs = %v, warns = %v", l.errs.Load(), l.warns.Load())
	}

	if c.EstimatedSize() > 1 {
		t.Fatal("the cache should not be empty")
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits > 2 ||
		snapshot.Misses != 0 ||
		snapshot.Loads() != 2 ||
		snapshot.LoadSuccesses != 1 ||
		snapshot.LoadFailures != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_BulkGetWithFailedLoad(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: ExpiryWriting[int, int](5 * time.Minute),
	})

	ks := []int{0, 1}
	someErr := errors.New("some error")
	tbl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		return nil, someErr
	})

	_, err := c.BulkGet(ctx, ks, tbl)
	if err != someErr {
		t.Fatalf("BulkGet error = %v; want someErr %v", err, someErr)
	}

	ch := make(chan struct{})
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
			ch <- struct{}{}
			ch <- struct{}{}
			return 0, someErr
		})
		_, err := c.Get(ctx, 0, tl)
		if err != someErr {
			t.Errorf("Get error = %v; want someErr %v", err, someErr)
			return
		}
	}()
	tbl = newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		<-ch
		res := make(map[int]int, len(keys))
		for _, k := range keys {
			res[k] = k + 100
		}
		return res, nil
	})
	<-ch
	res, err := c.BulkGet(ctx, ks, tbl)
	if err.Error() != someErr.Error() {
		t.Fatalf("BulkGet error = %v; want %v", err, someErr)
	}
	if len(res) != 1 {
		t.Fatal("result should contain only key = 1")
	}

	wg.Wait()

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits > 0 ||
		snapshot.Misses != 5 ||
		snapshot.Loads() != 3 ||
		snapshot.LoadSuccesses != 1 ||
		snapshot.LoadFailures != 2 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_BulkGetWithFailedRefresh(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	l := newTestLogger()
	fs := &fakeSource{}
	c := Must(&Options[int, int]{
		MaximumSize:   size,
		StatsRecorder: statsCounter,
		Clock:         fs,
		Executor: func(fn func()) {
			fn()
		},
		RefreshCalculator: RefreshWriting[int, int](time.Minute),
		Logger:            l,
	})

	ks := []int{0, 1}
	someErr := errors.New("some error")
	tbl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		return nil, someErr
	})

	for _, k := range ks {
		c.Set(k, 0)
	}
	fs.Sleep(time.Minute + time.Second)

	res, err := c.BulkGet(ctx, ks, tbl)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	for _, v := range res {
		if v != 0 {
			t.Fatalf("value = %v; want = %v", v, 0)
		}
	}

	if l.calls.Load() != 1 && l.errs.Load() != 1 {
		t.Fatalf("not valid logger stats. errs = %v, warns = %v", l.errs.Load(), l.warns.Load())
	}

	if c.EstimatedSize() > 2 {
		t.Fatal("the cache should not be empty")
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits > 2 ||
		snapshot.Misses != 0 ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses != 0 ||
		snapshot.LoadFailures != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_GetWithSuppressedLoad(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: ExpiryWriting[int, int](5 * time.Minute),
	})

	k1 := 1
	v1 := 100
	var wg1, wg2 sync.WaitGroup
	ch := make(chan int, 1)
	var calls atomic.Int32
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		if calls.Add(1) == 1 {
			// First invocation.
			wg1.Done()
		}
		v := <-ch
		ch <- v // pump; make available for any future calls

		time.Sleep(10 * time.Millisecond) // let more goroutines enter Load

		return v, nil
	})

	const n = 10
	wg1.Add(1)
	for i := 0; i < n; i++ {
		wg1.Add(1)
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			wg1.Done()
			v, err := c.Get(ctx, k1, tl)
			if err != nil {
				t.Errorf("Get error: %v", err)
				return
			}
			if v != v1 {
				t.Errorf("Get = %v; want %q", v, v1)
			}
		}()
	}
	wg1.Wait()
	// At least one goroutine is in loader now and all of them have at
	// least reached the line before the Loader.Load.
	ch <- v1
	wg2.Wait()
	if got := calls.Load(); got <= 0 || got >= n {
		t.Fatalf("number of calls = %d; want over 0 and less than %d", got, n)
	}

	if e, ok := c.GetEntryQuietly(k1); c.EstimatedSize() != 1 || ok && e.Value != v1 {
		t.Fatalf("the cache should only contain the key = %v", k1)
	}

	// wg1 only shows that a goroutine is about to call Get. One descheduled for longer than the
	// load calls Get after the value is stored and records a hit, so only the total is exact.
	snapshot := statsCounter.Snapshot()
	if snapshot.Hits+snapshot.Misses != n ||
		snapshot.Misses < 1 ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_ConcurrentGetAndSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: ExpiryWriting[int, int](5 * time.Minute),
	})

	ch := make(chan struct{})
	wg := sync.WaitGroup{}
	k1 := 1
	v1 := 100
	v2 := 101
	wg.Add(1)
	go func() {
		defer wg.Done()
		ch <- struct{}{}
		c.Set(k1, v1)
		ch <- struct{}{}
	}()
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		<-ch
		<-ch
		return v2, nil
	})
	v, err := c.Get(ctx, k1, tl)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if v != v1 {
		t.Fatalf("Get is not linerazable. want = %v, got = %v", v1, v)
	}

	e, ok := c.GetEntryQuietly(k1)
	require.True(t, ok)
	require.Equal(t, v1, e.Value)

	wg.Wait()

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 0 ||
		snapshot.Misses != 1 ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_ConcurrentLoadingAndInvalidate(t *testing.T) {
	t.Parallel()

	c := Must[int, int](&Options[int, int]{})

	key := 10
	value := key + 100
	ctx := context.Background()

	done := make(chan struct{})
	inv := make(chan struct{})
	var calls atomic.Uint64
	loader := LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
		firstCall := calls.Add(1) == 1
		if firstCall {
			done <- struct{}{}
		}
		time.Sleep(10 * time.Millisecond) // let more goroutines enter Load
		if firstCall {
			<-inv
		}
		return value, nil
	})

	var wg sync.WaitGroup
	goroutines := 10
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			v, err := c.Get(ctx, key, loader)
			require.NoError(t, err)
			require.Equal(t, value, v)
		}()
	}

	<-done
	// concurrent loading and invalidate
	c.Invalidate(key)
	inv <- struct{}{}

	wg.Wait()

	hasKey := c.has(key)
	if calls.Load() == 1 {
		require.False(t, hasKey)
	} else {
		require.True(t, hasKey)
	}
}

// Evicting an expired entry while a Get loads its new value must not cancel the load: the loaded
// value is stored, and the next read finds it (#188).
func TestCache_EvictionDoesNotCancelInFlightLoad(t *testing.T) {
	t.Parallel()

	clock := newNonTickingClock()
	// Maintenance waits for the gate, so that it evicts the expired entry only after the load
	// has started.
	gate := make(chan struct{})
	c := Must(&Options[int, int]{
		Clock:            clock,
		ExpiryCalculator: ExpiryWriting[int, int](time.Second),
		Executor: func(fn func()) {
			go func() {
				<-gate
				fn()
			}()
		},
	})
	c.Set(1, 1)
	clock.Sleep(2 * time.Second)

	started := make(chan struct{})
	release := make(chan struct{})
	type result struct {
		value int
		err   error
	}
	done := make(chan result, 1)
	go func() {
		v, err := c.Get(context.Background(), 1, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
			close(started)
			<-release
			return 2, nil
		}))
		done <- result{value: v, err: err}
	}()
	<-started

	close(gate)
	c.CleanUp()
	close(release)

	res := <-done
	require.NoError(t, res.err)
	require.Equal(t, 2, res.value)
	v, ok := c.GetIfPresent(1)
	require.True(t, ok, "the loaded value was not stored")
	require.Equal(t, 2, v)
}

// Get returns the value it found, not the one its own refresh wrote: the refresh may update the
// node in place before Get reads it (here with a synchronous executor).
func TestCache_GetReturnsValueBeforeInPlaceRefresh(t *testing.T) {
	t.Parallel()

	fs := &fakeSource{}
	c := Must(&Options[int, string]{
		MaximumSize: 10,
		Clock:       fs,
		Executor: func(fn func()) {
			fn()
		},
		RefreshCalculator: RefreshWriting[int, string](time.Minute),
	})
	c.Set(1, "a")
	c.Set(1, "b") // boxes the node, so that the refresh is applied in place

	fs.Sleep(2 * time.Minute)
	loader := LoaderFunc[int, string](func(ctx context.Context, key int) (string, error) {
		return "c", nil
	})
	v, err := c.Get(context.Background(), 1, loader)
	require.NoError(t, err)
	require.Equal(t, "b", v)

	v, ok := c.GetIfPresent(1)
	require.True(t, ok)
	require.Equal(t, "c", v)
}

// Evicting a node that was already replaced must not discard the refresh of the node that
// replaced it: the policy can pick the stale node as a victim before the replacement's update
// task is replayed.
func TestCache_StaleEvictionKeepsRefreshOfCurrentValue(t *testing.T) {
	t.Parallel()

	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprintf("stale=%v", stale), func(t *testing.T) {
			t.Parallel()

			c := Must(&Options[int, string]{
				MaximumSize:       2,
				RefreshCalculator: RefreshWriting[int, string](time.Hour),
			})
			ci := c.cache
			c.Set(1, "a") // a string is kept inline, so the next write replaces the node
			c.Set(2, "x")
			c.CleanUp()
			n1 := ci.hashmap.Get(1)
			require.True(t, n1.InMainProbation())

			ci.evictionMutex.Lock()
			ci.drainReadBuffer()
			ci.drainWriteBuffer()
			c.Set(3, "y")
			ci.drainWriteBuffer() // over the maximum now: 3 entries for 2

			// The write replaces n1 after the drain, so its update task stays in the buffer and
			// the retired n1 is still the first victim.
			c.Set(1, "b")
			require.True(t, n1.IsRetired())
			require.Equal(t, n1.AsPointer(), ci.evictionPolicy.probation.Head().AsPointer())

			started := make(chan struct{})
			release := make(chan struct{})
			ch := c.Refresh(context.Background(), 1, LoaderFunc[int, string](func(ctx context.Context, key int) (string, error) {
				close(started)
				<-release
				return "refreshed", nil
			}))
			<-started

			if !stale {
				// the update task is replayed first, so the victim is not stale
				ci.drainWriteBuffer()
			}
			ci.evictNodes()
			ci.evictionMutex.Unlock()

			close(release)
			res := <-ch
			require.NoError(t, res.Err)
			require.Equal(t, "refreshed", res.Value)
			c.CleanUp()

			v, ok := c.GetIfPresent(1)
			require.True(t, ok)
			require.Equal(t, "refreshed", v, "the refresh reported success, but its value was not stored")
		})
	}
}

// Evicting an entry drops the pending refresh of its value: a later Get loads the key instead
// of waiting for that refresh, and the refreshed value is not stored.
func TestCache_EvictionDropsRefreshOfEvictedValue(t *testing.T) {
	t.Parallel()

	c := Must(&Options[int, int]{
		MaximumSize:       10,
		RefreshCalculator: RefreshWriting[int, int](time.Hour),
	})
	ci := c.cache
	c.Set(1, 1)

	started := make(chan struct{})
	release := make(chan struct{})
	ch := c.Refresh(context.Background(), 1, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
		close(started)
		<-release
		return 3, nil
	}))
	<-started

	ci.evictionMutex.Lock()
	ci.evictionPolicy.evictNode(ci.hashmap.Get(1))
	ci.evictionMutex.Unlock()

	got := make(chan int, 1)
	go func() {
		v, err := c.Get(context.Background(), 1, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
			return 2, nil
		}))
		if err != nil {
			v = -1
		}
		got <- v
	}()
	select {
	case v := <-got:
		require.Equal(t, 2, v)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("Get waited for the refresh of an evicted entry")
	}

	close(release)
	<-ch
	c.CleanUp()
	v, ok := c.GetIfPresent(1)
	require.True(t, ok)
	require.Equal(t, 2, v, "the refresh of the evicted value was stored")
}

// A key that the bulk loader leaves out of its result is reported as not found, as when a
// Loader returns ErrNotFound: BulkGet leaves it out, BulkRefresh reports ErrNotFound, and a Get
// that joined the bulk load returns ErrNotFound instead of the zero value.
func TestCache_BulkLoadOmittedKeyIsNotFound(t *testing.T) {
	t.Parallel()

	t.Run("BulkGet", func(t *testing.T) {
		t.Parallel()

		c := Must[int, *int](nil)
		one := 1
		res, err := c.BulkGet(context.Background(), []int{1, 2}, BulkLoaderFunc[int, *int](func(ctx context.Context, keys []int) (map[int]*int, error) {
			return map[int]*int{1: &one}, nil
		}))
		require.NoError(t, err)
		require.Equal(t, map[int]*int{1: &one}, res)
		_, ok := c.GetIfPresent(2)
		require.False(t, ok)
	})

	t.Run("BulkRefresh", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{
			RefreshCalculator: RefreshWriting[int, int](time.Hour),
		})
		c.Set(1, 1)
		c.Set(2, 2)
		// Keys 1 and 2 are reloaded and key 3 is loaded, in separate bulk calls.
		results := <-c.BulkRefresh(context.Background(), []int{1, 2, 3}, BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
			res := make(map[int]int)
			for _, k := range keys {
				if k == 1 {
					res[k] = 10
				}
			}
			return res, nil
		}))
		require.Len(t, results, 3)
		for _, r := range results {
			if r.Key == 1 {
				require.NoError(t, r.Err)
				require.Equal(t, 10, r.Value)
			} else {
				require.ErrorIs(t, r.Err, ErrNotFound, "key %d", r.Key)
			}
		}
		for _, k := range []int{2, 3} {
			_, ok := c.GetIfPresent(k)
			require.False(t, ok)
		}
	})

	t.Run("joined Get", func(t *testing.T) {
		t.Parallel()

		// The Get has to start waiting on the bulk load before the bulk loader returns. It
		// cannot be observed, so the test retries until the Get did not load on its own.
		for range 20 {
			c := Must[int, int](nil)
			started := make(chan struct{})
			release := make(chan struct{})
			go func() {
				_, _ = c.BulkGet(context.Background(), []int{1, 2}, BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
					close(started)
					<-release
					return map[int]int{1: 10}, nil
				}))
			}()
			<-started

			var ownLoad atomic.Bool
			type result struct {
				v   int
				err error
			}
			got := make(chan result, 1)
			go func() {
				v, err := c.Get(context.Background(), 2, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
					ownLoad.Store(true)
					return 0, ErrNotFound
				}))
				got <- result{v: v, err: err}
			}()
			time.Sleep(10 * time.Millisecond)
			close(release)

			r := <-got
			if ownLoad.Load() {
				continue
			}
			require.Equal(t, 0, r.v)
			require.ErrorIs(t, r.err, ErrNotFound)
			return
		}
		t.Fatal("Get never joined the bulk load")
	})
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

// A refresh is registered when it is requested, not when the executor starts it, so a write
// that follows the request cancels it. Before, the refresh replaced a later Set, or brought an
// invalidated key back, whenever the executor started it after the write.
func TestCache_WriteAfterRefreshRequestCancelsIt(t *testing.T) {
	t.Parallel()

	reload := LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
		return 100, nil
	})
	tests := []struct {
		name string
		// request asks for a refresh of key 1, write then changes it
		request func(c *Cache[int, int])
		write   func(c *Cache[int, int])
		want    int
		present bool
	}{
		{
			name: "stale Get then Set",
			request: func(c *Cache[int, int]) {
				v, err := c.Get(context.Background(), 1, reload)
				require.NoError(t, err)
				require.Equal(t, 1, v)
			},
			write:   func(c *Cache[int, int]) { c.Set(1, 2) },
			want:    2,
			present: true,
		},
		{
			name: "stale Get then Invalidate",
			request: func(c *Cache[int, int]) {
				_, _ = c.Get(context.Background(), 1, reload)
			},
			write: func(c *Cache[int, int]) { c.Invalidate(1) },
		},
		{
			name: "stale BulkGet then Set",
			request: func(c *Cache[int, int]) {
				_, _ = c.BulkGet(context.Background(), []int{1}, BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
					return map[int]int{1: 100}, nil
				}))
			},
			write:   func(c *Cache[int, int]) { c.Set(1, 2) },
			want:    2,
			present: true,
		},
		{
			name: "Refresh then Compute",
			request: func(c *Cache[int, int]) {
				_ = c.Refresh(context.Background(), 1, reload)
			},
			write: func(c *Cache[int, int]) {
				_, _ = c.Compute(1, func(oldValue int, found bool) (int, ComputeOp) {
					return 3, WriteOp
				})
			},
			want:    3,
			present: true,
		},
		{
			name: "BulkRefresh then Invalidate",
			request: func(c *Cache[int, int]) {
				_ = c.BulkRefresh(context.Background(), []int{1}, BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
					return map[int]int{1: 100}, nil
				}))
			},
			write: func(c *Cache[int, int]) { c.Invalidate(1) },
		},
	}
	for _, tt := range tests {
		// Bounded, a write updates the node in place; unbounded, it replaces the node.
		for _, bounded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bounded=%v", tt.name, bounded), func(t *testing.T) {
				t.Parallel()

				clock := newNonTickingClock()
				executor := &queuedExecutor{}
				opts := &Options[int, int]{
					Clock:             clock,
					Executor:          executor.execute,
					RefreshCalculator: RefreshWriting[int, int](time.Second),
					Logger:            &recordingLogger{},
				}
				if bounded {
					opts.MaximumSize = 100
				}
				c := Must(opts)
				c.Set(1, 1)
				clock.Sleep(2 * time.Second)

				tt.request(c)
				tt.write(c)
				executor.run()

				v, ok := c.GetIfPresent(1)
				require.Equal(t, tt.present, ok)
				require.Equal(t, tt.want, v)
			})
		}
	}
}

// A refresh requested while a write is in progress, here from OnAtomicDeletion, which runs before
// the new value is published, is cancelled by that write: the write cancels pending calls only
// after it has published the value.
func TestCache_RefreshRequestedDuringWriteIsCancelled(t *testing.T) {
	t.Parallel()

	for _, bounded := range []bool{false, true} {
		t.Run(fmt.Sprintf("bounded=%v", bounded), func(t *testing.T) {
			t.Parallel()

			clock := newNonTickingClock()
			executor := &queuedExecutor{}
			var (
				c         *Cache[int, int]
				requested atomic.Bool
				// what the Get in the listener returned; checked after the Set, since a failed
				// assertion must not stop the goroutine that holds the bucket lock
				seen int
			)
			opts := &Options[int, int]{
				Clock:             clock,
				Executor:          executor.execute,
				RefreshCalculator: RefreshWriting[int, int](time.Second),
				Logger:            &recordingLogger{},
				OnAtomicDeletion: func(e DeletionEvent[int, int]) {
					if e.Cause != CauseReplacement || !requested.CompareAndSwap(false, true) {
						return
					}
					// Another goroutine still reads the previous, stale value and requests a
					// refresh of it.
					done := make(chan int)
					go func() {
						v, _ := c.Get(context.Background(), 1, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
							return 100, nil
						}))
						done <- v
					}()
					seen = <-done
				},
			}
			if bounded {
				opts.MaximumSize = 100
			}
			c = Must(opts)
			c.Set(1, 1)
			clock.Sleep(2 * time.Second)

			c.Set(1, 2)
			require.True(t, requested.Load())
			require.Equal(t, 1, seen, "the Get during the write did not read the previous value")
			executor.run()

			v, ok := c.GetIfPresent(1)
			require.True(t, ok)
			require.Equal(t, 2, v)
		})
	}
}

// A key that the bulk loader returns without being asked for only fills a gap: it is cached if the
// key has no value and is not being loaded when the bulk load completes. Nothing registered the
// key before the load, so a write during the load could not cancel it, and it used to overwrite
// that write.
func TestCache_BulkLoadExtraKeyOnlyFillsGaps(t *testing.T) {
	t.Parallel()

	// bulkGet runs a BulkGet of key 1 whose loader also returns key 2 with the value 100, and
	// calls during while the load is in progress.
	bulkGet := func(t *testing.T, c *Cache[int, int], during func()) {
		t.Helper()

		type result struct {
			res map[int]int
			err error
		}
		started := make(chan struct{})
		release := make(chan struct{})
		done := make(chan result, 1)
		go func() {
			res, err := c.BulkGet(context.Background(), []int{1}, BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
				close(started)
				<-release
				return map[int]int{1: 1, 2: 100}, nil
			}))
			done <- result{res: res, err: err}
		}()
		<-started
		during()
		close(release)
		r := <-done
		require.NoError(t, r.err)
		require.Equal(t, map[int]int{1: 1}, r.res)
	}

	t.Run("absent key is cached", func(t *testing.T) {
		t.Parallel()

		c := Must[int, int](nil)
		bulkGet(t, c, func() {})
		v, ok := c.GetIfPresent(2)
		require.True(t, ok)
		require.Equal(t, 100, v)
	})

	t.Run("existing value is kept", func(t *testing.T) {
		t.Parallel()

		c := Must[int, int](nil)
		c.Set(2, 2)
		bulkGet(t, c, func() {})
		v, ok := c.GetIfPresent(2)
		require.True(t, ok)
		require.Equal(t, 2, v)
	})

	t.Run("Set during the load wins", func(t *testing.T) {
		t.Parallel()

		c := Must[int, int](nil)
		bulkGet(t, c, func() { c.Set(2, 3) })
		v, ok := c.GetIfPresent(2)
		require.True(t, ok)
		require.Equal(t, 3, v)
	})

	t.Run("Compute during the load wins", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{MaximumSize: 100})
		c.Set(2, 2)
		bulkGet(t, c, func() {
			_, _ = c.Compute(2, func(oldValue int, found bool) (int, ComputeOp) {
				return oldValue + 1, WriteOp
			})
		})
		v, ok := c.GetIfPresent(2)
		require.True(t, ok)
		require.Equal(t, 3, v)
	})

	t.Run("Get load in flight wins", func(t *testing.T) {
		t.Parallel()

		c := Must[int, int](nil)
		getStarted := make(chan struct{})
		getRelease := make(chan struct{})
		getDone := make(chan int)
		bulkGet(t, c, func() {
			go func() {
				v, _ := c.Get(context.Background(), 2, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
					close(getStarted)
					<-getRelease
					return 4, nil
				}))
				getDone <- v
			}()
			<-getStarted
		})
		// the bulk load completed while the Get was still loading the key
		_, ok := c.GetIfPresent(2)
		require.False(t, ok, "the extra key was written over a load in flight")
		close(getRelease)
		require.Equal(t, 4, <-getDone)
		v, ok := c.GetIfPresent(2)
		require.True(t, ok)
		require.Equal(t, 4, v)
	})

	t.Run("BulkRefresh reports requested keys only", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{
			RefreshCalculator: RefreshWriting[int, int](time.Hour),
		})
		c.Set(1, 1)
		// key 1 is reloaded and key 2 loaded in separate bulk calls; both return key 3
		results := <-c.BulkRefresh(context.Background(), []int{1, 2}, BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
			res := map[int]int{3: 300}
			for _, k := range keys {
				res[k] = k * 10
			}
			return res, nil
		}))
		keys := make([]int, 0, len(results))
		for _, r := range results {
			keys = append(keys, r.Key)
		}
		require.ElementsMatch(t, []int{1, 2}, keys)
		v, ok := c.GetIfPresent(3)
		require.True(t, ok)
		require.Equal(t, 300, v)
	})
}

// InvalidateAll discards an entry whose node a refresh replaced after InvalidateAll took its
// snapshot of the nodes: it invalidates that key, not only the node it saw. The refreshes here
// complete from OnAtomicDeletion, between the snapshot and the removal of their keys.
func TestCache_InvalidateAllDiscardsEntriesReplacedDuringIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts *Options[int, string]
	}{
		// string values are stored inline when size-bounded, so the first update replaces the node
		{name: "bounded", opts: &Options[int, string]{MaximumSize: 1000}},
		// without maintenance every write replaces the node
		{name: "unbounded", opts: &Options[int, string]{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const n = 16
			var (
				armed   atomic.Bool
				fired   atomic.Bool
				release = make(chan struct{})
				started sync.WaitGroup
				// done[k] is closed when the refresh of k has completed
				done = make([]chan struct{}, n)

				mu          sync.Mutex
				invalidated = make(map[int]bool, n)
			)
			opts := *tt.opts
			opts.InitialCapacity = 4096
			opts.RefreshCalculator = RefreshWriting[int, string](time.Hour)
			opts.Logger = &recordingLogger{}
			opts.OnDeletion = func(e DeletionEvent[int, string]) {
				if e.Cause == CauseInvalidation {
					mu.Lock()
					invalidated[e.Key] = true
					mu.Unlock()
				}
			}
			opts.OnAtomicDeletion = func(e DeletionEvent[int, string]) {
				// The first removal by InvalidateAll lets the refreshes of the other keys
				// complete. A key in the bucket that this removal holds completes only after
				// the listener gives up on it.
				if !armed.Load() || e.Cause != CauseInvalidation || !fired.CompareAndSwap(false, true) {
					return
				}
				close(release)
				for k := 0; k < n; k++ {
					if k == e.Key {
						continue
					}
					select {
					case <-done[k]:
					case <-time.After(100 * time.Millisecond):
					}
				}
			}
			c := Must(&opts)
			for k := 0; k < n; k++ {
				c.Set(k, fmt.Sprint("v", k))
			}
			c.CleanUp()
			started.Add(n)
			for k := 0; k < n; k++ {
				done[k] = make(chan struct{})
				ch := c.Refresh(context.Background(), k, LoaderFunc[int, string](func(ctx context.Context, key int) (string, error) {
					started.Done()
					<-release
					return fmt.Sprint("refreshed", key), nil
				}))
				go func() {
					<-ch
					close(done[k])
				}()
			}
			started.Wait()

			armed.Store(true)
			c.InvalidateAll()
			armed.Store(false)
			for k := 0; k < n; k++ {
				<-done[k]
			}

			var survivors []int
			for k := 0; k < n; k++ {
				if _, ok := c.GetIfPresent(k); ok {
					survivors = append(survivors, k)
				}
			}
			require.Empty(t, survivors, "entries refreshed during InvalidateAll survived it")
			// every key reports its invalidation; OnDeletion runs on the executor
			require.Eventually(t, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(invalidated) == n
			}, 5*time.Second, 10*time.Millisecond, "a key's invalidation was not reported")
		})
	}
}

// Compute that cancels over an expired entry removes the entry, but keeps a load of the key that
// is in flight, which most likely started because the entry expired. Before, the load's value
// was returned to its caller but not cached, and the next Get loaded the key again.
func TestCache_ComputeCancelOverExpiredEntryKeepsLoad(t *testing.T) {
	t.Parallel()

	cancels := map[string]func(c *Cache[int, int]) (int, bool){
		"Compute": func(c *Cache[int, int]) (int, bool) {
			return c.Compute(1, func(oldValue int, found bool) (int, ComputeOp) {
				return 0, CancelOp
			})
		},
		"ComputeIfAbsent": func(c *Cache[int, int]) (int, bool) {
			return c.ComputeIfAbsent(1, func() (int, bool) {
				return 0, true
			})
		},
	}
	for name, cancel := range cancels {
		for _, bounded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bounded=%v", name, bounded), func(t *testing.T) {
				t.Parallel()

				c := newCacheWithExpiredEntry(t, bounded)
				var loads atomic.Int64
				started := make(chan struct{})
				release := make(chan struct{})
				loader := LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
					if loads.Add(1) == 1 {
						close(started)
						<-release
					}
					return 2, nil
				})
				got := make(chan int, 1)
				go func() {
					v, _ := c.Get(context.Background(), 1, loader)
					got <- v
				}()
				<-started

				v, ok := cancel(c)
				require.False(t, ok)
				require.Zero(t, v)

				close(release)
				require.Equal(t, 2, <-got)
				v, err := c.Get(context.Background(), 1, loader)
				require.NoError(t, err)
				require.Equal(t, 2, v)
				require.Equal(t, int64(1), loads.Load(), "the loaded value was not cached")
			})
		}
	}
}

// joinedLoadFrame matches the frame of a goroutine that waits for a load another goroutine runs.
var joinedLoadFrame = regexp.MustCompile(`\(\*call\[[^\]]*\]\)\.wait`)

// bucketLockFrame matches the frames of a goroutine that waits for a bucket lock of the hash table.
var bucketLockFrame = regexp.MustCompile(`\(\*Mutex\)\.Lock[\s\S]*hashmap\.\(\*Map\[[^\]]*\]\)\.Compute`)

// waitForJoinedLoad waits until a goroutine started by the running test waits for a load that
// another goroutine runs. Nothing else tells when a Get has joined a load, and a Get that comes
// too late does not join it but loads the key itself. Goroutines are told apart by the top-level
// test, so subtests that use it must not run in parallel.
func waitForJoinedLoad(t *testing.T) {
	t.Helper()
	waitForFrame(t, joinedLoadFrame, "no goroutine joined the load")
}

// waitForFrame waits until a goroutine started by the running test has a stack that frame
// matches, and fails with msg if none has after 10 seconds.
func waitForFrame(t *testing.T, frame *regexp.Regexp, msg string) {
	t.Helper()

	test, _, _ := strings.Cut(t.Name(), "/")
	createdBy := "otter/v2." + test + "."
	deadline := time.Now().Add(10 * time.Second)
	buf := make([]byte, 1<<20)
	for {
		size := runtime.Stack(buf, true)
		if size == len(buf) {
			buf = make([]byte, 2*len(buf))
			continue
		}
		for _, g := range strings.Split(string(buf[:size]), "\n\n") {
			if strings.Contains(g, createdBy) && frame.MatchString(g) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		runtime.Gosched()
	}
}

type getResult struct {
	value int
	err   error
}

func TestCache_GetWaiterReturnsWhenItsContextIsDone(t *testing.T) {
	t.Parallel()

	// A Get that joins another goroutine's load stops waiting when its own context is done.
	// The load goes on and caches its value.
	c := Must(&Options[int, int]{MaximumSize: 100})
	started := make(chan struct{})
	release := make(chan struct{})
	releaseLoad := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseLoad)
	tl := newTestLoader[int, int](func(_ context.Context, key int) (int, error) {
		close(started)
		<-release
		return key * 10, nil
	})

	owner := make(chan getResult, 1)
	go func() {
		v, err := c.Get(context.Background(), 1, tl)
		owner <- getResult{v, err}
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waiter := make(chan getResult, 1)
	go func() {
		v, err := c.Get(ctx, 1, tl)
		waiter <- getResult{v, err}
	}()
	select {
	case r := <-waiter:
		require.ErrorIs(t, r.err, context.Canceled)
		require.Zero(t, r.value)
	case <-time.After(5 * time.Second):
		t.Fatal("Get waits for the joined load after its context is done")
	}

	releaseLoad()
	r := <-owner
	require.NoError(t, r.err)
	require.Equal(t, 10, r.value)
	v, ok := c.GetIfPresent(1)
	require.True(t, ok)
	require.Equal(t, 10, v)
	require.Equal(t, uint64(1), tl.loads.Load())
}

func TestCache_BulkGetWaiterReturnsWhenItsContextIsDone(t *testing.T) {
	t.Parallel()

	c := Must(&Options[int, int]{MaximumSize: 100})
	started := make(chan struct{})
	release := make(chan struct{})
	releaseLoad := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseLoad)
	tl := newTestLoader[int, int](func(_ context.Context, key int) (int, error) {
		close(started)
		<-release
		return key * 10, nil
	})
	go func() {
		_, _ = c.Get(context.Background(), 1, tl)
	}()
	<-started

	// BulkGet loads key 2 itself and joins the load of key 1.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tbl := newTestBulkLoader[int, int](func(_ context.Context, keys []int) (map[int]int, error) {
		res := make(map[int]int, len(keys))
		for _, k := range keys {
			res[k] = k * 10
		}
		return res, nil
	})
	type bulkResult struct {
		values map[int]int
		err    error
	}
	done := make(chan bulkResult, 1)
	go func() {
		values, err := c.BulkGet(ctx, []int{1, 2}, tbl)
		done <- bulkResult{values, err}
	}()
	select {
	case r := <-done:
		require.ErrorIs(t, r.err, context.Canceled)
		// Only the values loaded by then: key 2 if BulkGet saw it first, never key 1.
		require.NotContains(t, r.values, 1)
		for k, v := range r.values {
			require.Equal(t, k*10, v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("BulkGet waits for the joined load after its context is done")
	}
	v, ok := c.GetIfPresent(2)
	require.True(t, ok)
	require.Equal(t, 20, v)

	releaseLoad()
	require.Eventually(t, func() bool {
		v, ok := c.GetIfPresent(1)
		return ok && v == 10
	}, 5*time.Second, time.Millisecond)
}

func TestCache_GetReloadsWhenOwnerContextFails(t *testing.T) {
	t.Parallel()

	// The goroutine that started a load gives up: its context is cancelled and the loader
	// fails. A joined Get with a live context loads the key again instead of returning that
	// failure. The loader's error does not wrap context.Canceled, as an RPC client's may not.
	c := Must(&Options[int, int]{MaximumSize: 100})
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	started := make(chan struct{})
	errCancelled := errors.New("rpc: cancelled")
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		if ctx == ownerCtx {
			close(started)
			<-ctx.Done()
			return 0, errCancelled
		}
		return key * 10, nil
	})

	owner := make(chan getResult, 1)
	go func() {
		v, err := c.Get(ownerCtx, 1, tl)
		owner <- getResult{v, err}
	}()
	<-started
	waiter := make(chan getResult, 1)
	go func() {
		v, err := c.Get(context.Background(), 1, tl)
		waiter <- getResult{v, err}
	}()
	waitForJoinedLoad(t)
	cancelOwner()

	require.ErrorIs(t, (<-owner).err, errCancelled)
	r := <-waiter
	require.NoError(t, r.err)
	require.Equal(t, 10, r.value)
	require.Equal(t, uint64(2), tl.loads.Load())
	v, ok := c.GetIfPresent(1)
	require.True(t, ok)
	require.Equal(t, 10, v)
}

func TestCache_BulkGetReloadsWhenOwnerContextFails(t *testing.T) {
	t.Parallel()

	c := Must(&Options[int, int]{MaximumSize: 100})
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	started := make(chan struct{})
	tl := newTestLoader[int, int](func(ctx context.Context, _ int) (int, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	})
	owner := make(chan error, 1)
	go func() {
		_, err := c.Get(ownerCtx, 1, tl)
		owner <- err
	}()
	<-started

	tbl := newTestBulkLoader[int, int](func(_ context.Context, keys []int) (map[int]int, error) {
		res := make(map[int]int, len(keys))
		for _, k := range keys {
			res[k] = k * 10
		}
		return res, nil
	})
	type bulkResult struct {
		values map[int]int
		err    error
	}
	waiter := make(chan bulkResult, 1)
	go func() {
		values, err := c.BulkGet(context.Background(), []int{1, 2}, tbl)
		waiter <- bulkResult{values, err}
	}()
	waitForJoinedLoad(t)
	cancelOwner()

	require.ErrorIs(t, <-owner, context.Canceled)
	r := <-waiter
	require.NoError(t, r.err)
	require.Equal(t, map[int]int{1: 10, 2: 20}, r.values)
	require.Equal(t, uint64(2), tbl.loads.Load())
}

func TestCache_GetWaiterSharesFailureOfTheKey(t *testing.T) {
	// Only a failure after the owner's context is done is retried. A failure while it is live,
	// and a panic of the loader, belong to the key and are shared with the joined Get. The
	// subtests are not parallel: waitForJoinedLoad cannot tell their goroutines apart.
	errLoad := errors.New("load failed")
	tests := []struct {
		name        string
		cancelOwner bool
		fail        func() error
		check       func(t *testing.T, err error)
	}{
		{
			name: "live owner context",
			fail: func() error { return errLoad },
			check: func(t *testing.T, err error) {
				t.Helper()
				require.ErrorIs(t, err, errLoad)
			},
		},
		{
			name:        "panic after the owner context is done",
			cancelOwner: true,
			fail:        func() error { panic("boom") },
			check: func(t *testing.T, err error) {
				t.Helper()
				var pe *panicError
				require.ErrorAs(t, err, &pe)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Must(&Options[int, int]{MaximumSize: 100})
			ownerCtx, cancelOwner := context.WithCancel(context.Background())
			defer cancelOwner()
			started := make(chan struct{})
			release := make(chan struct{})
			tl := newTestLoader[int, int](func(context.Context, int) (int, error) {
				close(started)
				<-release
				return 0, tt.fail()
			})
			go func() {
				defer func() { _ = recover() }()
				_, _ = c.Get(ownerCtx, 1, tl)
			}()
			<-started
			waiter := make(chan getResult, 1)
			go func() {
				v, err := c.Get(context.Background(), 1, tl)
				waiter <- getResult{v, err}
			}()
			waitForJoinedLoad(t)
			if tt.cancelOwner {
				cancelOwner()
			}
			close(release)

			tt.check(t, (<-waiter).err)
			require.Equal(t, uint64(1), tl.loads.Load())
		})
	}
}

func TestCache_BulkGetRetryTakesValueWrittenDuringFailedLoad(t *testing.T) {
	t.Parallel()

	// A key written while the joined load ran is in the cache when that load fails for its
	// owner's context, so BulkGet returns it instead of loading the key again over it.
	c := Must(&Options[int, int]{MaximumSize: 100})
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	started := make(chan struct{})
	tl := newTestLoader[int, int](func(ctx context.Context, _ int) (int, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	})
	go func() {
		_, _ = c.Get(ownerCtx, 1, tl)
	}()
	<-started

	tbl := newTestBulkLoader[int, int](func(_ context.Context, keys []int) (map[int]int, error) {
		res := make(map[int]int, len(keys))
		for _, k := range keys {
			res[k] = k * 10
		}
		return res, nil
	})
	type bulkResult struct {
		values map[int]int
		err    error
	}
	waiter := make(chan bulkResult, 1)
	go func() {
		values, err := c.BulkGet(context.Background(), []int{1}, tbl)
		waiter <- bulkResult{values, err}
	}()
	waitForJoinedLoad(t)
	c.Set(1, 99)
	cancelOwner()

	r := <-waiter
	require.NoError(t, r.err)
	require.Equal(t, map[int]int{1: 99}, r.values)
	require.Zero(t, tbl.loads.Load())
	v, ok := c.GetIfPresent(1)
	require.True(t, ok)
	require.Equal(t, 99, v)
}

func TestCache_ReadDoesNotRefreshAgain(t *testing.T) {
	t.Parallel()

	// A read sees a node stale and requests a refresh, but by the time the refresh wins its call
	// slot, the node has been refreshed by another read, or replaced: the refresh is skipped. A
	// removed key has no value to give, so it is refreshed as before.
	tests := []struct {
		name      string
		change    func(c *Cache[int, int], loader Loader[int, int])
		wantCalls uint64
	}{
		{
			name: "refreshed",
			change: func(c *Cache[int, int], loader Loader[int, int]) {
				<-c.Refresh(context.Background(), 1, loader)
			},
		},
		{
			name: "replaced",
			change: func(c *Cache[int, int], _ Loader[int, int]) {
				c.Invalidate(1)
				c.Set(1, 5)
			},
		},
		{
			name: "removed",
			change: func(c *Cache[int, int], _ Loader[int, int]) {
				c.Invalidate(1)
			},
			wantCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, bulk := range []bool{false, true} {
				clock := newNonTickingClock()
				c := Must(&Options[int, int]{
					MaximumSize:       100,
					RefreshCalculator: RefreshWriting[int, int](time.Minute),
					Clock:             clock,
					Executor:          func(fn func()) { fn() },
				})
				c.cache.singleflight.init()
				tl := newTestLoader[int, int](func(_ context.Context, key int) (int, error) {
					return key * 10, nil
				})
				tbl := newTestBulkLoader[int, int](func(_ context.Context, keys []int) (map[int]int, error) {
					res := make(map[int]int, len(keys))
					for _, k := range keys {
						res[k] = k * 10
					}
					return res, nil
				})

				c.Set(1, 1)
				clock.Sleep(2 * time.Minute)
				stale := c.cache.getNodeQuietly(1, c.cache.clock.NowNano())
				require.NotNil(t, stale)
				require.False(t, stale.IsFresh(c.cache.clock.NowNano()))

				tt.change(c, tl)
				before := tl.calls.Load() + tbl.calls.Load()
				rk := refreshableKey[int, int]{key: 1, old: stale}
				if bulk {
					c.cache.bulkRefreshKeys(context.Background(), []refreshableKey[int, int]{rk}, tbl, false)
				} else {
					c.cache.refreshKey(context.Background(), rk, tl, false)
				}
				require.Equal(t, before+tt.wantCalls, tl.calls.Load()+tbl.calls.Load(), "bulk=%v", bulk)
				require.Nil(t, c.cache.singleflight.getCall(1), "bulk=%v", bulk)
			}
		})
	}
}

func TestCache_RemovalDropsCommittedLoad(t *testing.T) {
	t.Parallel()

	// A load has written its value but is still registered when the entry is evicted or
	// expires. A Get after the removal must load the key again instead of joining the finished
	// load and getting back the removed value.
	tests := []struct {
		name   string
		remove func(c *Cache[int, int], clock *nonTickingClock)
	}{
		{
			name: "evicted",
			remove: func(c *Cache[int, int], _ *nonTickingClock) {
				c.cache.evictNode(c.cache.hashmap.Get(1))
			},
		},
		{
			name: "expired",
			remove: func(c *Cache[int, int], clock *nonTickingClock) {
				clock.Sleep(2 * time.Minute)
				c.cache.expireNode(c.cache.hashmap.Get(1), c.cache.clock.NowNano())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clock := newNonTickingClock()
			c := Must(&Options[int, int]{
				MaximumSize:      100,
				ExpiryCalculator: ExpiryWriting[int, int](time.Minute),
				Clock:            clock,
			})
			c.cache.singleflight.init()

			// The load writes its value; its call is deleted and its waiters woken only after.
			cl, shouldLoad := c.cache.singleflight.startLoad(1)
			require.True(t, shouldLoad)
			cl.value = 10
			c.cache.commitCall(cl)
			require.NotNil(t, c.cache.hashmap.Get(1))

			tt.remove(c, clock)
			require.Nil(t, c.cache.hashmap.Get(1))
			require.Nil(t, c.cache.singleflight.getCall(1), "the committed load is still registered")
			cl.cancel()

			tl := newTestLoader[int, int](func(_ context.Context, key int) (int, error) {
				return key * 20, nil
			})
			v, err := c.Get(context.Background(), 1, tl)
			require.NoError(t, err)
			require.Equal(t, 20, v)
			require.Equal(t, uint64(1), tl.loads.Load())
		})
	}
}

func TestCache_RemovalKeepsLoadInFlight(t *testing.T) {
	t.Parallel()

	// A load that has not committed yet is kept when the entry it replaces is evicted.
	c := Must(&Options[int, int]{MaximumSize: 100})
	c.Set(1, 1)
	c.cache.singleflight.init()
	cl, shouldLoad := c.cache.singleflight.startLoad(1)
	require.True(t, shouldLoad)

	c.cache.evictNode(c.cache.hashmap.Get(1))
	require.Same(t, cl, c.cache.singleflight.getCall(1))
	cl.cancel()
}

type panickingClock struct {
	*nonTickingClock
	panics atomic.Bool
}

func (c *panickingClock) NowNano() int64 {
	if c.panics.Load() {
		panic("clock")
	}
	return c.nonTickingClock.NowNano()
}

func TestCache_ClockPanicDoesNotStrandRefresh(t *testing.T) {
	t.Parallel()

	// The clock is user code. If it panics while a read requests a refresh, the panic is logged,
	// and the refresh must not stay registered: every later refresh of the key would join it, and
	// none would ever run.
	for _, bulk := range []bool{false, true} {
		clock := &panickingClock{nonTickingClock: newNonTickingClock()}
		c := Must(&Options[int, int]{
			MaximumSize:       100,
			RefreshCalculator: RefreshWriting[int, int](time.Minute),
			Clock:             clock,
			Executor:          func(fn func()) { fn() },
		})
		c.cache.singleflight.init()
		tl := newTestLoader[int, int](func(_ context.Context, key int) (int, error) {
			return key * 10, nil
		})
		tbl := newTestBulkLoader[int, int](func(_ context.Context, keys []int) (map[int]int, error) {
			res := make(map[int]int, len(keys))
			for _, k := range keys {
				res[k] = k * 10
			}
			return res, nil
		})

		c.Set(1, 1)
		clock.Sleep(2 * time.Minute)
		stale := c.cache.getNodeQuietly(1, c.cache.clock.NowNano())
		require.NotNil(t, stale)

		clock.panics.Store(true)
		rk := refreshableKey[int, int]{key: 1, old: stale}
		require.NotPanics(t, func() {
			if bulk {
				c.cache.bulkRefreshKeys(context.Background(), []refreshableKey[int, int]{rk}, tbl, false)
			} else {
				c.cache.refreshKey(context.Background(), rk, tl, false)
			}
		}, "bulk=%v", bulk)
		clock.panics.Store(false)
		require.Nil(t, c.cache.singleflight.getCall(1), "bulk=%v", bulk)
	}
}

func TestCache_LoadKeepsValueWrittenBeforeItsRegistration(t *testing.T) {
	t.Parallel()

	// A write cancels the loads registered for its key before it stores its value, so a Get
	// that misses the key and registers a load in between is not cancelled. The load then finds
	// the written value when it completes: the value stays, and the load returns it.
	tests := []struct {
		name    string
		loadErr error
	}{
		{name: "loaded"},
		{name: "not found", loadErr: ErrNotFound},
		{name: "failed", loadErr: errors.New("load failed")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, bulk := range []bool{false, true} {
				c := Must(&Options[int, int]{})
				c.cache.singleflight.init()

				// The load is registered after the write: as if the Get had missed the key before
				// the write stored its value.
				c.Set(1, 2)
				cl, shouldLoad := c.cache.singleflight.startLoad(1)
				require.True(t, shouldLoad)

				if bulk {
					_ = c.cache.singleflight.doBulkCall(context.Background(), map[int]*call[int, int]{1: cl},
						func(context.Context, []int) (map[int]int, error) {
							if tt.loadErr != nil {
								return nil, tt.loadErr
							}
							return map[int]int{1: 1}, nil
						})
				} else {
					_ = c.cache.singleflight.doCall(context.Background(), cl, func(context.Context, int) (int, error) {
						return 1, tt.loadErr
					})
				}

				v, ok := c.GetIfPresent(1)
				require.True(t, ok, "bulk=%v", bulk)
				require.Equal(t, 2, v, "bulk=%v", bulk)
				require.NoError(t, cl.err, "bulk=%v", bulk)
				require.Equal(t, 2, cl.value, "bulk=%v", bulk)
			}
		})
	}
}

func TestCache_ReadsDuringRefreshSubmitNoTasks(t *testing.T) {
	t.Parallel()

	// While a refresh of a key is in flight, a read that finds the key stale has nothing to do.
	// A task that only waited for the refresh would take an executor slot for the whole reload:
	// with an executor that runs a bounded number of tasks, enough stale reads take every slot,
	// the reads block on the submission, and so does the refresh queued behind them.
	ctx := context.Background()
	clock := newNonTickingClock()
	var submitted atomic.Int64
	c := Must(&Options[int, int]{
		RefreshCalculator: RefreshWriting[int, int](time.Minute),
		Clock:             clock,
		Executor: func(fn func()) {
			submitted.Add(1)
			go fn()
		},
	})
	release := make(chan struct{})
	tl := newTestLoader[int, int](func(context.Context, int) (int, error) {
		<-release
		return 2, nil
	})
	tbl := newTestBulkLoader[int, int](func(context.Context, []int) (map[int]int, error) {
		<-release
		return map[int]int{1: 2}, nil
	})

	c.Set(1, 1)
	clock.Sleep(2 * time.Minute)
	v, err := c.Get(ctx, 1, tl)
	require.NoError(t, err)
	require.Equal(t, 1, v)
	require.Equal(t, int64(1), submitted.Load())

	for range 10 {
		v, err := c.Get(ctx, 1, tl)
		require.NoError(t, err)
		require.Equal(t, 1, v)
		res, err := c.BulkGet(ctx, []int{1}, tbl)
		require.NoError(t, err)
		require.Equal(t, map[int]int{1: 1}, res)
	}
	// A manual refresh that joins the in-flight one still reports its result.
	single := c.Refresh(ctx, 1, tl)
	bulk := c.BulkRefresh(ctx, []int{1}, tbl)
	require.Equal(t, int64(1), submitted.Load())

	close(release)
	require.Equal(t, RefreshResult[int, int]{Key: 1, Value: 2}, <-single)
	require.Equal(t, []RefreshResult[int, int]{{Key: 1, Value: 2}}, <-bulk)
	require.Equal(t, uint64(1), tl.calls.Load()+tbl.calls.Load())
}

func TestCache_ReadDuringInvalidateStartsNoRefresh(t *testing.T) {
	t.Parallel()

	// A read that finds the node while an invalidation removes it sees the node stale, since a
	// removed node is not fresh. A refresh of it would be discarded anyway, and it would hold
	// the key's call slot for loads that start after the invalidation.
	ctx := context.Background()
	release := make(chan struct{})
	defer close(release)
	tl := newTestLoader[int, int](func(context.Context, int) (int, error) {
		<-release
		return 2, nil
	})
	tbl := newTestBulkLoader[int, int](func(context.Context, []int) (map[int]int, error) {
		<-release
		return map[int]int{1: 2}, nil
	})
	for _, bulk := range []bool{false, true} {
		var c *Cache[int, int]
		c = Must(&Options[int, int]{
			// nodes are retired on removal only in a cache with maintenance
			MaximumSize:       100,
			RefreshCalculator: RefreshWriting[int, int](time.Minute),
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				if e.Cause != CauseInvalidation {
					return
				}
				if bulk {
					_, _ = c.BulkGet(ctx, []int{e.Key}, tbl)
				} else {
					_, _ = c.Get(ctx, e.Key, tl)
				}
			},
		})

		c.Set(1, 1)
		c.Invalidate(1)
		require.Nil(t, c.cache.singleflight.getCall(1), "bulk=%v", bulk)
		require.Zero(t, tl.calls.Load()+tbl.calls.Load(), "bulk=%v", bulk)
	}
}

func TestCache_CallSupersedesRefreshOfRemovedNode(t *testing.T) {
	t.Parallel()

	// A refresh that was registered after its base had been removed or replaced cannot write
	// its result, so it is not joined: the next call for the key replaces it. A Get after an
	// invalidation would otherwise get the reload of the invalidated value, and a stale
	// replacing node would not be refreshed until the doomed refresh had finished.
	tests := []struct {
		name string
		// replaced writes the key again before the doomed refresh is registered
		replaced bool
		run      func(t *testing.T, c *Cache[int, int], doomed *call[int, int])
	}{
		{
			name: "Get",
			run: func(t *testing.T, c *Cache[int, int], _ *call[int, int]) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				v, err := c.Get(ctx, 1, newTestLoader[int, int](func(context.Context, int) (int, error) {
					return 5, nil
				}))
				require.NoError(t, err)
				require.Equal(t, 5, v)
			},
		},
		{
			name: "BulkGet",
			run: func(t *testing.T, c *Cache[int, int], _ *call[int, int]) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				res, err := c.BulkGet(ctx, []int{1}, newTestBulkLoader[int, int](func(context.Context, []int) (map[int]int, error) {
					return map[int]int{1: 5}, nil
				}))
				require.NoError(t, err)
				require.Equal(t, map[int]int{1: 5}, res)
			},
		},
		{
			name:     "refresh of the replacing node",
			replaced: true,
			run: func(t *testing.T, c *Cache[int, int], doomed *call[int, int]) {
				n := c.cache.getNodeQuietly(1, c.cache.clock.NowNano())
				cl, shouldLoad := c.cache.singleflight.startRefresh(1, n)
				require.True(t, shouldLoad)
				require.NotSame(t, doomed, cl)
				require.Same(t, cl, c.cache.singleflight.getCall(1))
				c.cache.singleflight.adopt(cl, n.Value(), 0, n)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := Must(&Options[int, int]{
				RefreshCalculator: RefreshWriting[int, int](time.Minute),
			})
			c.cache.singleflight.init()
			c.Set(1, 1)
			base := c.cache.getNodeQuietly(1, c.cache.clock.NowNano())
			c.Invalidate(1)
			if tt.replaced {
				c.Set(1, 3)
			}
			// The writes have run before the refresh was registered, so they did not cancel it.
			doomed, shouldLoad := c.cache.singleflight.startRefresh(1, base)
			require.True(t, shouldLoad)
			defer doomed.cancel()

			tt.run(t, c, doomed)
		})
	}
}

func TestCache_ExtraBulkEntryKeepsInvalidation(t *testing.T) {
	t.Parallel()

	// A bulk loader may return keys that were not asked for. They have no call of their own for an
	// invalidation to cancel, so an extra value that the loader read before the key was
	// invalidated must not be cached after the invalidation has returned.
	tests := []struct {
		name    string
		present bool
		bulk    func(ctx context.Context, c *Cache[int, int], bl BulkLoader[int, int])
	}{
		{
			name: "BulkGet, absent",
			bulk: func(ctx context.Context, c *Cache[int, int], bl BulkLoader[int, int]) {
				_, _ = c.BulkGet(ctx, []int{1}, bl)
			},
		},
		{
			name:    "BulkGet, present",
			present: true,
			bulk: func(ctx context.Context, c *Cache[int, int], bl BulkLoader[int, int]) {
				_, _ = c.BulkGet(ctx, []int{1}, bl)
			},
		},
		{
			name: "BulkRefresh",
			bulk: func(ctx context.Context, c *Cache[int, int], bl BulkLoader[int, int]) {
				<-c.BulkRefresh(ctx, []int{1}, bl)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := Must(&Options[int, int]{
				RefreshCalculator: RefreshWriting[int, int](time.Minute),
			})
			if tt.present {
				c.Set(2, 1)
			}
			started := make(chan struct{})
			proceed := make(chan struct{})
			bl := newTestBulkLoader[int, int](func(context.Context, []int) (map[int]int, error) {
				close(started)
				<-proceed
				return map[int]int{1: 10, 2: 20}, nil
			})

			done := make(chan struct{})
			go func() {
				defer close(done)
				tt.bulk(context.Background(), c, bl)
			}()
			<-started
			c.Invalidate(2)
			close(proceed)
			<-done

			v, ok := c.GetIfPresent(1)
			require.True(t, ok)
			require.Equal(t, 10, v)
			v, ok = c.GetIfPresent(2)
			require.False(t, ok, "the invalidated key holds %d", v)
		})
	}
}

type splitLoader struct {
	load   func(ctx context.Context, key int) (int, error)
	reload func(ctx context.Context, key, oldValue int) (int, error)
}

func (l splitLoader) Load(ctx context.Context, key int) (int, error) { return l.load(ctx, key) }

func (l splitLoader) Reload(ctx context.Context, key, oldValue int) (int, error) {
	return l.reload(ctx, key, oldValue)
}

func TestCache_GetJoinedRefreshNotWritten(t *testing.T) {
	t.Parallel()

	// A Get that misses a key may join a refresh of it. If the cache does not write the
	// refresh's result, the Get returns the key's value in the cache, or loads the key itself:
	// the result it waited for is not a value of the key.
	ctx := context.Background()
	load5 := func(context.Context, int) (int, error) { return 5, nil }
	tests := []struct {
		name string
		// start registers a refresh, and change, called once a Get has joined it, makes the cache
		// refuse its result
		start  func(c *Cache[int, int], clock *nonTickingClock)
		change func(c *Cache[int, int], clock *nonTickingClock)
		want   int
	}{
		{
			name: "refresh of an absent key, cancelled by SetIfAbsent",
			start: func(c *Cache[int, int], _ *nonTickingClock) {
				c.Refresh(ctx, 1, splitLoader{load: func(context.Context, int) (int, error) {
					return 2, nil
				}})
			},
			change: func(c *Cache[int, int], _ *nonTickingClock) {
				_, inserted := c.SetIfAbsent(1, 3)
				require.True(t, inserted)
			},
			want: 3,
		},
		{
			name: "refresh whose node expires",
			start: func(c *Cache[int, int], clock *nonTickingClock) {
				c.Set(1, 1)
				clock.Sleep(30 * time.Second)
				v, err := c.Get(ctx, 1, splitLoader{reload: func(context.Context, int, int) (int, error) {
					return 2, nil
				}})
				require.NoError(t, err)
				require.Equal(t, 1, v)
				clock.Sleep(time.Minute)
			},
			change: func(*Cache[int, int], *nonTickingClock) {},
			want:   5,
		},
	}
	for _, tt := range tests {
		for _, bulk := range []bool{false, true} {
			// Tasks wait in a queue until run: the refresh until a Get has joined it, and the
			// maintenance, which would remove an expired node and drop its refresh before the Get
			// joins it.
			var (
				mu    sync.Mutex
				tasks []func()
			)
			run := func() {
				for {
					mu.Lock()
					if len(tasks) == 0 {
						mu.Unlock()
						return
					}
					fn := tasks[0]
					tasks = tasks[1:]
					mu.Unlock()
					fn()
				}
			}
			clock := newNonTickingClock()
			c := Must(&Options[int, int]{
				ExpiryCalculator:  ExpiryWriting[int, int](time.Minute),
				RefreshCalculator: RefreshWriting[int, int](10 * time.Second),
				Clock:             clock,
				Executor: func(fn func()) {
					mu.Lock()
					defer mu.Unlock()
					tasks = append(tasks, fn)
				},
			})
			tt.start(c, clock)

			got := make(chan getResult, 1)
			go func() {
				if bulk {
					res, err := c.BulkGet(ctx, []int{1}, newTestBulkLoader[int, int](func(context.Context, []int) (map[int]int, error) {
						return map[int]int{1: 5}, nil
					}))
					got <- getResult{res[1], err}
					return
				}
				v, err := c.Get(ctx, 1, splitLoader{load: load5})
				got <- getResult{v, err}
			}()
			waitForJoinedLoad(t)
			tt.change(c, clock)
			run()

			r := <-got
			require.NoError(t, r.err, "%s, bulk=%v", tt.name, bulk)
			require.Equal(t, tt.want, r.value, "%s, bulk=%v", tt.name, bulk)
			v, ok := c.GetIfPresent(1)
			require.True(t, ok, "%s, bulk=%v", tt.name, bulk)
			require.Equal(t, tt.want, v, "%s, bulk=%v", tt.name, bulk)
		}
	}
}

// shortReadExpiry gives entries a minute and moves their deadline to 10 seconds on a read.
type shortReadExpiry struct{}

func (shortReadExpiry) ExpireAfterCreate(Entry[int, int]) time.Duration { return time.Minute }

func (shortReadExpiry) ExpireAfterUpdate(Entry[int, int], int) time.Duration { return time.Minute }

func (shortReadExpiry) ExpireAfterRead(Entry[int, int]) time.Duration { return 10 * time.Second }

func TestCache_GetDoesNotTakeLoadedValueExpiredBeforeIt(t *testing.T) {
	t.Parallel()

	// A load hands its waiters a value from the cache before its call is deleted: the value it
	// wrote, the value written during the load, or one written by another load before it started
	// (adopt). A Get that misses the key in between, because the value has already expired,
	// joins the call: it must not return a value that expired before it started. The value
	// expires at the deadline the call recorded with it, or earlier if SetExpiresAfter or a read
	// whose ExpireAfterRead shortens the lifetime moves the deadline meanwhile.
	ctx := context.Background()
	starts := []struct {
		name string
		// start registers the load and runs the part of it that finds or writes its value; the
		// returned func finishes it
		start func(c *Cache[int, int]) (finish func())
	}{
		{
			name: "written",
			start: func(c *Cache[int, int]) func() {
				cl, _ := c.cache.singleflight.startLoad(1)
				cl.value = 1
				m := c.cache.commitCall(cl)
				return func() {
					c.cache.singleflight.deleteCall(cl)
					cl.cancel()
					c.cache.publish(m)
				}
			},
		},
		{
			name: "written during the load",
			start: func(c *Cache[int, int]) func() {
				c.Set(1, 3)
				cl, _ := c.cache.singleflight.startLoad(1)
				cl.value = 1
				m := c.cache.commitCall(cl)
				return func() {
					c.cache.singleflight.deleteCall(cl)
					cl.cancel()
					c.cache.publish(m)
				}
			},
		},
		{
			name: "adopted",
			start: func(c *Cache[int, int]) func() {
				c.Set(1, 3)
				cl, _ := c.cache.singleflight.startLoad(1)
				value, expiresAt, n := c.cache.readForAdoption(cl, c.cache.clock.NowNano())
				return func() {
					c.cache.singleflight.adopt(cl, value, expiresAt, n)
				}
			},
		},
	}
	expirations := []struct {
		name   string
		expire func(c *Cache[int, int], clock *nonTickingClock)
	}{
		{name: "recorded deadline", expire: func(c *Cache[int, int], clock *nonTickingClock) {
			clock.Sleep(2 * time.Minute)
		}},
		{name: "SetExpiresAfter", expire: func(c *Cache[int, int], clock *nonTickingClock) {
			c.SetExpiresAfter(1, 10*time.Second)
			clock.Sleep(20 * time.Second)
		}},
		{name: "ExpireAfterRead", expire: func(c *Cache[int, int], clock *nonTickingClock) {
			c.GetIfPresent(1)
			clock.Sleep(20 * time.Second)
		}},
	}
	for _, st := range starts {
		for _, ex := range expirations {
			for _, bulk := range []bool{false, true} {
				name := fmt.Sprintf("%s, %s, bulk=%v", st.name, ex.name, bulk)
				clock := newNonTickingClock()
				c := Must(&Options[int, int]{
					ExpiryCalculator: shortReadExpiry{},
					Clock:            clock,
					// the tasks never run: maintenance would remove the expired entry, and with it the call
					Executor: func(func()) {},
				})
				c.cache.singleflight.init()
				finish := st.start(c)
				ex.expire(c, clock)

				got := make(chan getResult, 1)
				go func() {
					if bulk {
						res, err := c.BulkGet(ctx, []int{1}, newTestBulkLoader[int, int](func(context.Context, []int) (map[int]int, error) {
							return map[int]int{1: 5}, nil
						}))
						got <- getResult{res[1], err}
						return
					}
					v, err := c.Get(ctx, 1, newTestLoader[int, int](func(context.Context, int) (int, error) {
						return 5, nil
					}))
					got <- getResult{v, err}
				}()
				waitForJoinedLoad(t)
				finish()

				r := <-got
				require.NoError(t, r.err, name)
				require.Equal(t, 5, r.value, name)
			}
		}
	}
}

func TestCache_GetDoesNotReturnLoadOfInvalidatedKey(t *testing.T) {
	t.Parallel()

	// An invalidation cancels the load of its key. When the load completes, the key has no
	// value, so the loaded value is not written, and it is not returned either: it was never in
	// the cache, and Invalidate has reported the key absent. The key is loaded again.
	ctx := context.Background()
	tests := []struct {
		name string
		get  func(c *Cache[int, int], load LoaderFunc[int, int]) (int, error)
		// waiter makes another goroutine own the load, so that the Get waits for it
		waiter bool
	}{
		{name: "Get", get: func(c *Cache[int, int], load LoaderFunc[int, int]) (int, error) {
			return c.Get(ctx, 1, load)
		}},
		{name: "BulkGet", get: func(c *Cache[int, int], load LoaderFunc[int, int]) (int, error) {
			res, err := c.BulkGet(ctx, []int{1}, BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
				v, err := load(ctx, keys[0])
				return map[int]int{keys[0]: v}, err
			}))
			return res[1], err
		}},
		{name: "waiting Get", waiter: true, get: func(c *Cache[int, int], load LoaderFunc[int, int]) (int, error) {
			return c.Get(ctx, 1, load)
		}},
	}
	for _, tt := range tests {
		c := Must(&Options[int, int]{})
		started := make(chan struct{})
		release := make(chan struct{})
		var loads atomic.Int64
		load := LoaderFunc[int, int](func(context.Context, int) (int, error) {
			if loads.Add(1) == 1 {
				close(started)
				<-release
				return 1, nil
			}
			return 2, nil
		})

		got := make(chan getResult, 1)
		if tt.waiter {
			go func() { _, _ = c.Get(ctx, 1, load) }()
			<-started
			go func() {
				v, err := tt.get(c, load)
				got <- getResult{v, err}
			}()
			waitForJoinedLoad(t)
		} else {
			go func() {
				v, err := tt.get(c, load)
				got <- getResult{v, err}
			}()
			<-started
		}
		_, invalidated := c.Invalidate(1)
		require.False(t, invalidated, tt.name)
		close(release)

		r := <-got
		require.NoError(t, r.err, tt.name)
		require.Equal(t, 2, r.value, tt.name)
		v, ok := c.GetIfPresent(1)
		require.True(t, ok, tt.name)
		require.Equal(t, 2, v, tt.name)
	}
}

func TestCache_WriteCancelsCallRegisteredWhileItIsApplied(t *testing.T) {
	t.Parallel()

	// An invalidation cancels the calls of its key before the hash table removes the node. A
	// Get that registers a load in between still finds the old value on its re-check and adopts
	// it, and a Get that starts after the invalidation returned would join that call and get
	// the invalidated value. The call is cancelled again once the change is visible.
	tests := []struct {
		name       string
		invalidate func(c *Cache[int, int])
	}{
		{name: "Invalidate", invalidate: func(c *Cache[int, int]) { c.Invalidate(1) }},
		{name: "Compute", invalidate: func(c *Cache[int, int]) {
			c.Compute(1, func(int, bool) (int, ComputeOp) { return 0, InvalidateOp })
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				c        *Cache[int, int]
				adopting *call[int, int]
				sawOld   bool
			)
			c = Must(&Options[int, int]{
				OnAtomicDeletion: func(DeletionEvent[int, int]) {
					// A Get registers its load and re-checks the table: the node is still there.
					// The listener runs under the bucket lock, so nothing here may fail the test.
					adopting, _ = c.cache.singleflight.startLoad(1)
					sawOld = c.cache.getNodeQuietly(1, c.cache.clock.NowNano()) != nil
				},
			})
			c.cache.singleflight.init()
			c.Set(1, 1)
			tt.invalidate(c)
			require.NotNil(t, adopting)
			defer c.cache.singleflight.adopt(adopting, 1, 0, nil)
			require.True(t, sawOld)

			require.Nil(t, c.cache.singleflight.getCall(1))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			v, err := c.Get(ctx, 1, LoaderFunc[int, int](func(context.Context, int) (int, error) { return 5, nil }))
			require.NoError(t, err)
			require.Equal(t, 5, v)
		})
	}
}

func TestCache_EvictionDropsLoadThatAdoptedItsValue(t *testing.T) {
	t.Parallel()

	// A load that wins its call slot and finds a value in the cache adopts it. If the value is
	// evicted before the call is finished, a Get that starts after the eviction must not join
	// the call and get the evicted value.
	ctx := context.Background()
	load99 := LoaderFunc[int, int](func(context.Context, int) (int, error) { return 99, nil })

	t.Run("evicted after the read", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{MaximumSize: 100})
		c.cache.singleflight.init()
		c.Set(1, 1)
		n := c.cache.getNodeQuietly(1, c.cache.clock.NowNano())
		cl, _ := c.cache.singleflight.startLoad(1)
		value, expiresAt, live := c.cache.readForAdoption(cl, c.cache.clock.NowNano())
		require.NotNil(t, live)
		require.Equal(t, 1, value)

		c.cache.evictNode(n) // before the call is finished
		require.Nil(t, c.cache.singleflight.getCall(1))
		v, err := c.Get(ctx, 1, load99)
		require.NoError(t, err)
		require.Equal(t, 99, v)
		c.cache.singleflight.adopt(cl, value, expiresAt, live)
	})
	t.Run("evicted before the read", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{MaximumSize: 100})
		c.cache.singleflight.init()
		c.Set(1, 1)
		n := c.cache.getNodeQuietly(1, c.cache.clock.NowNano())
		cl, _ := c.cache.singleflight.startLoad(1)
		c.cache.evictNode(n) // after the lock-free check found the value
		_, _, live := c.cache.readForAdoption(cl, c.cache.clock.NowNano())
		require.Nil(t, live)
		c.cache.singleflight.deleteCall(cl)
		cl.cancel()
	})
}
