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

			for k := range ks {
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
	// not yet been published, and start a redundant second load. afterDeleteCall
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

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits != 0 ||
		snapshot.Misses != n ||
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
	if v != v2 {
		t.Fatalf("Get is not linerazable. want = %v, got = %v", v2, v)
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
