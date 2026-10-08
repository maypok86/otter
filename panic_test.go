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
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/stats"
)

// recordingLogger records the messages logged at the error level.
type recordingLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (l *recordingLogger) Warn(ctx context.Context, msg string, err error) {}

func (l *recordingLogger) Error(ctx context.Context, msg string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, msg)
}

func (l *recordingLogger) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.msgs...)
}

// completes fails the test if fn does not return within a few seconds, which is how a lock
// left held by a panic shows up.
func completes(t *testing.T, name string, fn func()) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not complete: a lock or a load was left behind", name)
	}
}

func syncExecutor(fn func()) {
	fn()
}

// A panicking weigher or expiry calculator propagates to the caller, does not apply the write,
// and leaves the bucket usable.
func TestCache_PanicInCalculatorDoesNotApplyTheWrite(t *testing.T) {
	t.Parallel()

	c := Must(&Options[int, int]{
		MaximumWeight: 100,
		Weigher: func(key, value int) uint32 {
			if value < 0 {
				panic("weigher boom")
			}
			return 1
		},
		ExpiryCalculator: ExpiryWriting[int, int](time.Hour),
	})
	c.Set(1, 1)

	require.PanicsWithValue(t, "weigher boom", func() { c.Set(1, -1) })
	require.PanicsWithValue(t, "weigher boom", func() { c.Set(2, -1) })
	completes(t, "Set after a panicking weigher", func() {
		v, ok := c.GetIfPresent(1)
		require.True(t, ok)
		require.Equal(t, 1, v)
		_, ok = c.GetIfPresent(2)
		require.False(t, ok)

		c.Set(1, 2)
		c.Set(2, 2)
		c.CleanUp()
	})
	v, _ := c.GetIfPresent(1)
	require.Equal(t, 2, v)
}

// A panicking ExpireAfterRead on SetIfAbsent of a live key runs under the bucket lock.
func TestCache_PanicInExpireAfterReadUnderBucketLock(t *testing.T) {
	t.Parallel()

	var fail sync.Map
	c := Must(&Options[int, int]{
		ExpiryCalculator: &expiryFunc{
			read: func(e Entry[int, int]) time.Duration {
				if _, ok := fail.Load(e.Key); ok {
					panic("read boom")
				}
				return time.Hour
			},
		},
	})
	c.Set(1, 1)
	fail.Store(1, struct{}{})
	require.PanicsWithValue(t, "read boom", func() { c.SetIfAbsent(1, 2) })
	fail.Delete(1)
	completes(t, "Set after a panicking ExpireAfterRead", func() { c.Set(1, 3) })
}

// expiryFunc is an ExpiryCalculator with a custom ExpireAfterRead.
type expiryFunc struct {
	read func(e Entry[int, int]) time.Duration
}

func (e *expiryFunc) ExpireAfterCreate(entry Entry[int, int]) time.Duration { return time.Hour }

func (e *expiryFunc) ExpireAfterUpdate(entry Entry[int, int], oldValue int) time.Duration {
	return time.Hour
}

func (e *expiryFunc) ExpireAfterRead(entry Entry[int, int]) time.Duration { return e.read(entry) }

// A panic of a deletion listener or a stats recorder is logged: the operation completes and the
// cache stays consistent, also when the panic happens during maintenance.
func TestCache_PanicInListenersAndStatsIsLogged(t *testing.T) {
	t.Parallel()

	logger := &recordingLogger{}
	c := Must(&Options[int, int]{
		MaximumSize: 10,
		Executor:    syncExecutor,
		Logger:      logger,
		OnDeletion: func(e DeletionEvent[int, int]) {
			panic("on deletion boom")
		},
		OnAtomicDeletion: func(e DeletionEvent[int, int]) {
			panic("on atomic deletion boom")
		},
		StatsRecorder: &panickingRecorder{Counter: stats.NewCounter()},
	})

	completes(t, "writes with panicking listeners", func() {
		for i := 0; i < 1000; i++ {
			c.Set(i%50, i)
			c.Compute(i%50, func(old int, found bool) (int, ComputeOp) {
				return i, WriteOp
			})
			if i%7 == 0 {
				c.Invalidate(i % 50)
			}
		}
		c.CleanUp()
	})
	require.LessOrEqual(t, c.EstimatedSize(), 10)
	validatePolicy(t, c)

	msgs := logger.get()
	require.Contains(t, msgs, "OnDeletion panicked")
	require.Contains(t, msgs, "OnAtomicDeletion panicked")
	require.Contains(t, msgs, "StatsRecorder.RecordHits panicked")
	require.Contains(t, msgs, "StatsRecorder.RecordEviction panicked")
}

type panickingRecorder struct {
	*stats.Counter
}

func (r *panickingRecorder) RecordHits(count int) { panic("stats boom") }

func (r *panickingRecorder) RecordEviction(weight uint32) { panic("stats boom") }

// A panicking custom executor does not leave the eviction lock held.
func TestCache_PanicInExecutorDoesNotStopMaintenance(t *testing.T) {
	t.Parallel()

	logger := &recordingLogger{}
	c := Must(&Options[int, int]{
		MaximumSize: 10,
		Logger:      logger,
		Executor: func(fn func()) {
			panic("executor boom")
		},
		OnDeletion: func(e DeletionEvent[int, int]) {},
	})

	completes(t, "writes with a panicking executor", func() {
		for i := 0; i < 5000; i++ {
			c.Set(i, i)
		}
		c.CleanUp()
		_ = c.GetMaximum()
	})
	require.LessOrEqual(t, c.EstimatedSize(), 10)
	require.NotEmpty(t, logger.get())
}

// A panic while writing a loaded value (here the weigher) wakes up the waiters, which receive
// the panic as the load's error, and a later load of the key works.
func TestCache_PanicWhileWritingLoadedValueWakesWaiters(t *testing.T) {
	t.Parallel()

	var failing sync.Map
	c := Must(&Options[int, int]{
		MaximumWeight: 100,
		Weigher: func(key, value int) uint32 {
			if _, ok := failing.Load(key); ok {
				panic("weigher boom")
			}
			return 1
		},
	})

	failing.Store(1, struct{}{})
	release := make(chan struct{})
	started := make(chan struct{})
	loader := LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
		close(started)
		<-release
		return 42, nil
	})

	leader := make(chan any, 1)
	go func() {
		defer func() { leader <- recover() }()
		_, _ = c.Get(context.Background(), 1, loader)
	}()
	<-started

	waiter := make(chan error, 1)
	go func() {
		_, err := c.Get(context.Background(), 1, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
			return 7, nil
		}))
		waiter <- err
	}()
	// give the waiter time to join the load
	time.Sleep(50 * time.Millisecond)
	close(release)

	require.Equal(t, "weigher boom", <-leader)
	select {
	case err := <-waiter:
		// the waiter either joined the failed load or started a load of its own
		if err != nil {
			var pe *panicError
			require.ErrorAs(t, err, &pe)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter of a load whose result could not be written was never woken up")
	}

	failing.Delete(1)
	completes(t, "Get after the failed load", func() {
		v, err := c.Get(context.Background(), 1, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
			return 8, nil
		}))
		require.NoError(t, err)
		require.Contains(t, []int{7, 8}, v)
	})
}

// A panic while writing one result of a bulk load finishes every call of the bulk, so that no
// later load of the other keys blocks forever.
func TestCache_PanicWhileWritingBulkResultFinishesAllCalls(t *testing.T) {
	t.Parallel()

	c := Must(&Options[int, int]{
		MaximumWeight: 100,
		Weigher: func(key, value int) uint32 {
			if key == 3 && value == 1 {
				panic("weigher boom")
			}
			return 1
		},
	})
	bulkLoader := BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		res := make(map[int]int, len(keys))
		for _, k := range keys {
			res[k] = 1
		}
		return res, nil
	})

	require.Panics(t, func() {
		_, _ = c.BulkGet(context.Background(), []int{1, 2, 3, 4, 5}, bulkLoader)
	})
	for k := 1; k <= 5; k++ {
		completes(t, "Get after a panicking bulk load", func() {
			v, err := c.Get(context.Background(), k, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
				return 2, nil
			}))
			require.NoError(t, err)
			require.Contains(t, []int{1, 2}, v)
		})
	}
}

// A panicking loader during a refresh does not crash the process: the result carries the panic
// as its error, the old value stays, and the error is logged.
func TestCache_PanicInRefreshIsReturnedAsError(t *testing.T) {
	t.Parallel()

	logger := &recordingLogger{}
	c := Must(&Options[int, int]{
		MaximumSize:       10,
		Logger:            logger,
		RefreshCalculator: RefreshWriting[int, int](time.Hour),
	})
	c.Set(1, 1)

	loader := LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
		panic("reload boom")
	})
	var res RefreshResult[int, int]
	completes(t, "Refresh with a panicking loader", func() {
		res = <-c.Refresh(context.Background(), 1, loader)
	})
	var pe *panicError
	require.ErrorAs(t, res.Err, &pe)
	require.Equal(t, "reload boom", pe.value)

	v, ok := c.GetIfPresent(1)
	require.True(t, ok)
	require.Equal(t, 1, v)
	require.Contains(t, logger.get(), "Returned an error during the refreshing")

	bulkLoader := BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		panic("bulk reload boom")
	})
	var results []RefreshResult[int, int]
	completes(t, "BulkRefresh with a panicking loader", func() {
		results = <-c.BulkRefresh(context.Background(), []int{1, 2}, bulkLoader)
	})
	require.Len(t, results, 2)
	for _, r := range results {
		require.ErrorAs(t, r.Err, &pe)
	}
}

// A loader that calls runtime.Goexit does not produce a value: the waiters get an error and
// nothing is cached.
func TestCache_GoexitInLoader(t *testing.T) {
	t.Parallel()

	c := Must(&Options[int, int]{MaximumSize: 10})

	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = c.Get(context.Background(), 1, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
			close(started)
			<-release
			runtime.Goexit()
			return 0, nil
		}))
	}()
	<-started

	waiter := make(chan error, 1)
	go func() {
		_, err := c.Get(context.Background(), 1, LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
			return 0, errors.New("the waiter must not load")
		}))
		waiter <- err
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)

	select {
	case err := <-waiter:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter was never woken up")
	}
	_, ok := c.GetIfPresent(1)
	require.False(t, ok, "the zero value of an exited load was cached")
}

// validatePolicy checks that every entry is linked into the eviction policy and accounted.
func validatePolicy(t *testing.T, c *Cache[int, int]) {
	t.Helper()

	ci := c.cache
	ci.evictionMutex.Lock()
	defer ci.evictionMutex.Unlock()
	ci.maintenance(nil)

	p := ci.evictionPolicy
	linked := p.window.Len() + p.probation.Len() + p.protected.Len()
	require.Equal(t, ci.hashmap.Size(), linked, "entries outside the policy")
	require.Equal(t, uint64(linked), p.weightedSize)
}

type panickingLogger struct{}

func (panickingLogger) Warn(context.Context, string, error) { panic("logger boom") }

func (panickingLogger) Error(context.Context, string, error) { panic("logger boom") }

// The cache logs from its recovery paths and from refresh goroutines. A panicking Logger must
// not undo the recovery or crash the caller.
func TestCache_PanicInLoggerIsContained(t *testing.T) {
	t.Parallel()

	t.Run("refresh error", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{
			MaximumSize:       10,
			Logger:            panickingLogger{},
			Executor:          syncExecutor,
			RefreshCalculator: RefreshWriting[int, int](time.Hour),
		})
		c.Set(1, 1)

		loader := LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
			return 0, errors.New("reload failed")
		})
		var res RefreshResult[int, int]
		require.NotPanics(t, func() {
			res = <-c.Refresh(context.Background(), 1, loader)
		})
		require.EqualError(t, res.Err, "reload failed")
	})
	t.Run("executor panic", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{
			MaximumSize: 10,
			Logger:      panickingLogger{},
			Executor: func(fn func()) {
				panic("executor boom")
			},
		})
		completes(t, "writes with a panicking executor and logger", func() {
			require.NotPanics(t, func() {
				for i := 0; i < 5000; i++ {
					c.Set(i, i)
				}
				c.CleanUp()
			})
			_ = c.GetMaximum()
		})
		require.LessOrEqual(t, c.EstimatedSize(), 10)
	})
}
