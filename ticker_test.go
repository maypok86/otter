//go:build !go1.27

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
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const tickerChildEnv = "OTTER_TEST_TICKER_CHILD"

// TestCache_BuiltInClockStopsTicker checks that the periodic goroutine stops the ticker of the
// built-in clocks. With GODEBUG=asynctimerchan=1, an unstopped ticker stays in the timer heap
// for the life of the process, with its timer, channel and buffer. The setting is process-wide,
// so the check runs in a child process.
func TestCache_BuiltInClockStopsTicker(t *testing.T) {
	if os.Getenv(tickerChildEnv) == "1" {
		checkBuiltInClockStopsTicker(t)
		return
	}
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCache_BuiltInClockStopsTicker$", "-test.count=1")
	godebug := "asynctimerchan=1"
	if v := os.Getenv("GODEBUG"); v != "" {
		godebug = v + "," + godebug
	}
	cmd.Env = append(os.Environ(), tickerChildEnv+"=1", "GODEBUG="+godebug)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
}

func checkBuiltInClockStopsTicker(t *testing.T) {
	const n = 1000

	clocks := map[string]func() Clock{
		"default": func() Clock { return nil },
		"coarse":  func() Clock { return NewCoarseClock(time.Second) },
	}
	cases := []struct {
		name         string
		drop, goexit bool
	}{
		{name: "stopped"},
		{name: "dropped", drop: true},
		// the ticker passes to the goroutine that takes over after a Goexit
		{name: "stopped after a Goexit", goexit: true},
	}
	// warms up the runtime's pools
	startAndEndCaches(t, n, nil, false, true)
	for name, newClock := range clocks {
		for _, tc := range cases {
			before := liveHeapObjects()
			startAndEndCaches(t, n, newClock(), tc.drop, tc.goexit)
			perCache := float64(int64(liveHeapObjects())-int64(before)) / n
			// A leaked ticker keeps 3 heap objects; a stopped one keeps none.
			require.LessOrEqual(t, perCache, 1.5, "clock %s, %s: heap objects kept per cache", name, tc.name)
		}
	}
}

// startAndEndCaches creates n caches with expiration and waits until their periodic goroutines
// return: after StopAllGoroutines, or once the dropped caches are collected. With goexit, the
// entry of each cache expires on its periodic goroutine, and its OnAtomicDeletion calls
// runtime.Goexit there before the cache is stopped.
func startAndEndCaches(t *testing.T, n int, clock Clock, drop, goexit bool) {
	t.Helper()

	var exited atomic.Int64
	expiry := time.Hour
	var onDeletion func(DeletionEvent[int, int])
	if goexit {
		// longer than the drain after Set, so that the periodic goroutine removes the entry
		expiry = 100 * time.Millisecond
		onDeletion = func(DeletionEvent[int, int]) {
			if strings.Contains(string(debug.Stack()), "otter/v2.periodicCleanUp[") {
				exited.Add(1)
				runtime.Goexit()
			}
		}
	}
	caches := make([]*Cache[int, int], 0, n)
	for i := range n {
		c := Must(&Options[int, int]{
			ExpiryCalculator: ExpiryWriting[int, int](expiry),
			OnAtomicDeletion: onDeletion,
			Clock:            clock,
		})
		c.Set(i, i)
		caches = append(caches, c)
	}
	deadline := time.Now().Add(10 * time.Second)
	for exited.Load() < int64(len(caches)) && goexit {
		require.False(t, time.Now().After(deadline), "%d of %d entries removed on the periodic goroutine", exited.Load(), n)
		time.Sleep(50 * time.Millisecond)
	}
	if !drop {
		for _, c := range caches {
			c.StopAllGoroutines()
		}
	}
	if cc, ok := clock.(*CoarseClock); ok {
		cc.Stop()
	}
	for periodicGoroutines() > 0 {
		require.False(t, time.Now().After(deadline), "periodic goroutines did not return")
		runtime.GC()
		time.Sleep(100 * time.Millisecond)
	}
}

func periodicGoroutines() int {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	return strings.Count(string(buf), "otter/v2.periodicCleanUp[")
}

func liveHeapObjects() uint64 {
	for range 3 {
		runtime.GC()
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapObjects
}
