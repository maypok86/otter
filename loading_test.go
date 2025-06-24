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

	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	fs := &fakeSource{}
	c := Must(&Options[int, int]{
		MaximumSize:       size,
		StatsRecorder:     statsCounter,
		Clock:             fs,
		RefreshCalculator: RefreshWriting[int, int](10 * time.Minute),
	})

	k1 := 1
	v1 := 100
	v2 := 101
	c.Set(k1, v1)

	fs.Sleep(10*time.Minute + time.Second)
	done := make(chan struct{})
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		if key == k1 {
			done <- struct{}{}
			return v2, nil
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

	<-done
	if cl := c.cache.singleflight.getCall(k1); cl != nil {
		cl.wait()
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
		MaximumSize:       size,
		StatsRecorder:     statsCounter,
		Clock:             fs,
		RefreshCalculator: RefreshWriting[int, int](time.Hour + time.Minute),
	})

	k1 := 1
	v1 := 100
	c.Set(k1, v1)

	fs.Sleep(time.Hour + time.Minute + time.Second)
	someErr := fmt.Errorf("olololo: %w", ErrNotFound)
	done := make(chan struct{})
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		if key == k1 {
			done <- struct{}{}
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

	<-done
	if cl := c.cache.singleflight.getCall(k1); cl != nil {
		cl.wait()
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
	ctx := context.Background()

	done := make(chan struct{})
	tl1 := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		<-done
		if key == k1 {
			return v1, nil
		}
		panic("not valid key")
	})
	tl2 := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		if key == k1 {
			return v2, nil
		}
		panic("not valid key")
	})

	ch := c.Refresh(ctx, k1, tl1)

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
	if snapshot.Hits != 3 ||
		snapshot.Misses != 2 ||
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

	ctx := context.Background()
	statsCounter := stats.NewCounter()
	fs := &fakeSource{}
	c := Must(&Options[int, int]{
		MaximumSize:       size,
		StatsRecorder:     statsCounter,
		Clock:             fs,
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
	done := make(chan struct{})
	tl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		calls.Add(1)

		m := make(map[int]int, len(keys))
		for _, k := range keys {
			if calls.Load() == 1 && ks[k] {
				t.Fatalf("key should not be loaded. key: %v", k)
			}
			m[k] = k + 100
		}
		done <- struct{}{}
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
	<-done
	for _, k := range keys {
		if cl := c.cache.singleflight.getCall(k); cl != nil {
			cl.wait()
		}
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
		MaximumSize:       size,
		StatsRecorder:     statsCounter,
		Clock:             fs,
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
	done := make(chan struct{})
	tl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		m := make(map[int]int, len(keys))
		for _, k := range keys {
			if k == notFound {
				continue
			}
			m[k] = k + 100
		}
		done <- struct{}{}
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
	<-done
	for _, k := range keys {
		if cl := c.cache.singleflight.getCall(k); cl != nil {
			cl.wait()
		}
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
		MaximumSize:       size,
		StatsRecorder:     statsCounter,
		Clock:             fs,
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
	done := make(chan struct{})
	tl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		m := make(map[int]int, len(keys))
		for _, k := range keys {
			m[k] = k + 100
		}
		m[fake] = fake + 100
		done <- struct{}{}
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
	<-done
	for _, k := range keys {
		if cl := c.cache.singleflight.getCall(k); cl != nil {
			cl.wait()
		}
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
	ctx := context.Background()

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
		return m, nil
	})

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
	if snapshot.Hits != 18 ||
		snapshot.Misses != 13 ||
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

	done := make(chan struct{})
	var startRefresh atomic.Bool
	c.cache.executor = func(fn func()) {
		go func() {
			if startRefresh.Load() {
				<-done
				startRefresh.Store(false)
			}
			fn()
		}()
	}

	ctx := context.Background()
	waitLoad := make(chan struct{})
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		done <- struct{}{}
		if key == toLoad {
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

	go func() {
		<-done
		v, err := c.Get(ctx, toLoad, tl)
		require.NoError(t, err)
		require.Equal(t, toLoad+101, v)
		done <- struct{}{}
	}()

	startRefresh.Store(true)
	done <- struct{}{}
	ch := c.BulkRefresh(ctx, keys, btl)
	results := <-ch

	require.Equal(t, len(keys), len(results))
	<-done
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
	if snapshot.Hits != 5 ||
		snapshot.Misses != 2 ||
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
		MaximumSize:       size,
		StatsRecorder:     statsCounter,
		Clock:             fs,
		RefreshCalculator: RefreshCreating[int, int](5 * time.Second),
		Logger:            l,
	})

	k1 := 1
	someErr := errors.New("some error")
	done := make(chan struct{})
	tl := newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		done <- struct{}{}
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

	<-done
	if cl := c.cache.singleflight.getCall(k1); cl != nil {
		cl.wait()
	}

	if l.calls.Load() != 1 && l.errs.Load() != 1 {
		t.Fatalf("not valid logger stats. errs = %v, warns = %v", l.errs.Load(), l.warns.Load())
	}

	v, err = c.Get(ctx, k1, newTestLoader[int, int](func(ctx context.Context, key int) (int, error) {
		done <- struct{}{}
		return 0, ErrNotFound
	}))
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if v != 0 {
		t.Fatalf("Get value = %v; want = %v", v, 0)
	}

	<-done
	if cl := c.cache.singleflight.getCall(k1); cl != nil {
		cl.wait()
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
		MaximumSize:       size,
		StatsRecorder:     statsCounter,
		Clock:             fs,
		RefreshCalculator: RefreshWriting[int, int](time.Minute),
		Logger:            l,
	})

	ks := []int{0, 1}
	someErr := errors.New("some error")
	done := make(chan struct{})
	tbl := newTestBulkLoader[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		done <- struct{}{}
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

	<-done
	for _, k := range ks {
		if cl := c.cache.singleflight.getCall(k); cl != nil {
			cl.wait()
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
	loader := LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
		done <- struct{}{}
		<-inv
		return value, nil
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()

		v, err := c.Get(ctx, key, loader)
		require.NoError(t, err)
		require.Equal(t, value, v)
	}()

	<-done
	// concurrent loading and invalidate
	c.Invalidate(key)
	inv <- struct{}{}

	wg.Wait()

	require.False(t, c.has(key))
}
