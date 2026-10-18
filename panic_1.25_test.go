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

// An OnAtomicDeletion that calls runtime.Goexit while the periodic clean-up expires an entry ends
// that clean-up's goroutine, not the periodic clean-ups: later ticks still expire entries of an
// idle cache, the one whose removal was interrupted too, and StopAllGoroutines still stops them.
func TestCache_GoexitInOnAtomicDeletionKeepsPeriodicCleanUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var exit atomic.Bool
		exit.Store(true)
		var expired atomic.Int64
		c := Must(&Options[int, int]{
			ExpiryCalculator: ExpiryWriting[int, int](time.Second),
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				if e.Cause != CauseExpiration {
					return
				}
				if exit.CompareAndSwap(true, false) {
					runtime.Goexit()
				}
				expired.Add(1)
			},
		})
		defer func() {
			require.True(t, c.StopAllGoroutines())
		}()

		c.Set(1, 1)
		time.Sleep(3 * time.Second)
		synctest.Wait()
		require.False(t, exit.Load(), "the periodic clean-up did not expire the entry")

		// Nothing but the periodic clean-up runs maintenance after this write's drain, which
		// happens before the entry expires.
		c.Set(2, 2)
		synctest.Wait()
		time.Sleep(3 * time.Second)
		synctest.Wait()
		require.Equal(t, int64(2), expired.Load(), "the periodic clean-up has stopped")
		require.Nil(t, c.cache.hashmap.Get(1), "the interrupted removal was not retried")
		require.Nil(t, c.cache.hashmap.Get(2))
	})
}
