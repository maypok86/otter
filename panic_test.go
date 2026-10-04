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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
