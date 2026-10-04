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
