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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maypok86/otter/v2/stats"
)

type errValue struct{}

func (err *errValue) Error() string {
	return "error value"
}

type testLoader[K comparable, V any] struct {
	fn    func(ctx context.Context, key K) (V, error)
	calls atomic.Uint64
}

func newTestLoader[K comparable, V any](fn func(ctx context.Context, key K) (V, error)) *testLoader[K, V] {
	return &testLoader[K, V]{fn: fn}
}

func (tl *testLoader[K, V]) Load(ctx context.Context, key K) (V, error) {
	tl.calls.Add(1)
	return tl.fn(ctx, key)
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
			c, err := NewBuilder[int, int]().
				MaximumSize(size).
				WithTTL(5 * time.Minute).
				RecordStats(statsCounter).
				Build()
			if err != nil {
				t.Fatalf("can not create cache: %v", err)
			}

			k1 := 1
			var recovered any

			func() {
				defer func() {
					recovered = recover()
					t.Logf("after panic(%#v) in cache.Get, recovered %#v", tt.panicValue, recovered)
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

			if c.singleflight.getCall(k1) != nil || c.Size() > 0 {
				t.Fatal("the call should be deleted even in case of panic")
			}
			if c.Size() > 0 {
				t.Fatal("the cache should be empty after panic")
			}
		})
	}
}

func TestCache_GetWithSuccessLoad(t *testing.T) {
	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithTTL(5 * time.Minute).
		RecordStats(statsCounter).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

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

	if c.Size() != 1 || c.GetNodeQuietly(k1).Value() != v1 {
		t.Fatalf("the cache should only contain the key = %v", k1)
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits() != 1 ||
		snapshot.Misses() != 1 ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses() != 1 {
		t.Fatal("statistics are not recorded correctly")
	}
}

func TestCache_GetWithFailedLoad(t *testing.T) {
	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithTTL(5 * time.Minute).
		RecordStats(statsCounter).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

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

	if c.Size() > 0 {
		t.Fatal("the cache should be empty")
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits() > 0 ||
		snapshot.Misses() != 2 ||
		snapshot.Loads() != 2 ||
		snapshot.LoadSuccesses() != 1 ||
		snapshot.LoadFailures() != 1 {
		t.Fatal("statistics are not recorded correctly")
	}
}

func TestCache_GetWithSuppressedLoad(t *testing.T) {
	ctx := context.Background()
	size := 100
	statsCounter := stats.NewCounter()
	c, err := NewBuilder[int, int]().
		MaximumSize(size).
		WithTTL(5 * time.Minute).
		RecordStats(statsCounter).
		Build()
	if err != nil {
		t.Fatalf("can not create cache: %v", err)
	}

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

	if c.Size() != 1 || c.GetNodeQuietly(k1).Value() != v1 {
		t.Fatalf("the cache should only contain the key = %v", k1)
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits() != 0 ||
		snapshot.Misses() != n ||
		snapshot.Loads() != 1 ||
		snapshot.LoadSuccesses() != 1 {
		t.Fatal("statistics are not recorded correctly")
	}
}
