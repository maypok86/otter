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

package otter

import (
	"runtime"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCoarseClock_UpdatesOncePerResolution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewCoarseClock(10 * time.Millisecond)
		defer c.Stop()

		start := time.Now().UnixNano()
		require.Equal(t, start, c.NowNano())

		time.Sleep(5 * time.Millisecond)
		require.Equal(t, start, c.NowNano(), "the clock moved before a tick")

		time.Sleep(20 * time.Millisecond)
		require.Equal(t, start+int64(20*time.Millisecond), c.NowNano())
	})
}

func TestCoarseClock_PreciseAfterStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewCoarseClock(10 * time.Millisecond)
		time.Sleep(15 * time.Millisecond)
		c.Stop()
		c.Stop()

		select {
		case <-c.done:
		default:
			t.Fatal("Stop returned before the goroutine")
		}
		time.Sleep(time.Millisecond)
		require.Equal(t, time.Now().UnixNano(), c.NowNano())
	})
}

func TestCoarseClock_ZeroValueIsPrecise(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var c CoarseClock
		require.Equal(t, time.Now().UnixNano(), c.NowNano())
		time.Sleep(time.Millisecond)
		require.Equal(t, time.Now().UnixNano(), c.NowNano())
		c.Stop()
	})
}

func TestCoarseClock_CollectedInSynctestBubble(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := NewCoarseClock(time.Millisecond).done

		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			require.Less(t, i, 10, "the goroutine of an unreachable clock did not stop")
			// Wait for the update of the last tick, which holds the clock, to finish.
			synctest.Wait()
			runtime.GC()
			time.Sleep(time.Millisecond)
		}
	})
}

func TestCoarseClock_Expiration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := NewCoarseClock(10 * time.Millisecond)
		defer clock.Stop()

		c := Must(&Options[int, int]{
			ExpiryCalculator: ExpiryWriting[int, int](time.Second),
			Clock:            clock,
		})
		defer c.StopAllGoroutines()
		require.IsType(t, coarseSource{}, c.cache.clock)

		// The write lands between ticks, so its deadline is a second after the last tick, 5ms
		// before a second after the write.
		time.Sleep(5 * time.Millisecond)
		c.Set(1, 1)
		time.Sleep(990 * time.Millisecond)
		_, ok := c.GetIfPresent(1)
		require.True(t, ok, "the entry expired before the clock reached its deadline")

		time.Sleep(7 * time.Millisecond)
		_, ok = c.GetIfPresent(1)
		require.False(t, ok, "the entry did not expire at the tick that reached its deadline")
	})
}
