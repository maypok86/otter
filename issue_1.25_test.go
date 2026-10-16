//go:build go1.25

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
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// https://github.com/maypok86/otter/issues/158
func TestCache_Issue158(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := Must(&Options[string, string]{
			ExpiryCalculator: ExpiryAccessing[string, string](time.Minute),
		})
		defer func() {
			require.True(t, cache.StopAllGoroutines())
		}()

		cache.Set("key", "value")
	})
}

// A cache with expiration that is created in a synctest bubble and collected there, without
// StopAllGoroutines, does not crash the program: nothing outside the bubble touches its
// channels, which is a fatal error, and its periodic clean-up goroutine returns at the next tick.
func TestCache_CollectedInSynctestBubble(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cleaned atomic.Bool
		func() {
			c := Must(&Options[int, int]{
				ExpiryCalculator: ExpiryWriting[int, int](time.Minute),
			})
			c.Set(1, 1)
			runtime.AddCleanup(c.cache, func(b *atomic.Bool) {
				b.Store(true)
			}, &cleaned)
		}()

		for i := 0; !cleaned.Load(); i++ {
			require.Less(t, i, 10, "the cache was not collected")
			// Wait for a clean-up of the last tick, which holds the cache, to finish.
			synctest.Wait()
			runtime.GC()
			time.Sleep(time.Second)
		}
		// The goroutine returns at its next tick; a goroutine left behind fails synctest.Test.
		time.Sleep(time.Second)
	})
}
