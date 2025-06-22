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
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Clock is a time source that
//   - Returns a time value representing the number of nanoseconds elapsed since some
//     fixed but arbitrary point in time
//   - Returns a channel that delivers “ticks” of a clock at intervals.
type Clock interface {
	// NowNano returns the number of nanoseconds elapsed since this clock's fixed point of reference.
	//
	// By default, time.Now().UnixNano() is used.
	NowNano() int64
	// Tick returns a channel that delivers “ticks” of a clock at intervals.
	//
	// The cache uses this method only for proactive expiration and calls Tick(time.Second) in a separate goroutine.
	//
	// By default, [time.Tick] is used.
	Tick(duration time.Duration) <-chan time.Time
}

type timeSource interface {
	Clock
	Init()
	Sleep(duration time.Duration)
}

func newTimeSource(clock Clock) timeSource {
	if clock == nil {
		return &realSource{}
	}
	if r, ok := clock.(*realSource); ok {
		return r
	}
	return newCustomSource(clock)
}

type customSource struct {
	clock         Clock
	isInitialized atomic.Bool
}

func newCustomSource(clock Clock) *customSource {
	return &customSource{
		clock: clock,
	}
}

func (cs *customSource) Init() {
	if !cs.isInitialized.Load() {
		cs.isInitialized.Store(true)
	}
}

func (cs *customSource) NowNano() int64 {
	if !cs.isInitialized.Load() {
		return 0
	}
	return cs.clock.NowNano()
}

func (cs *customSource) Tick(duration time.Duration) <-chan time.Time {
	return time.Tick(duration)
}

func (cs *customSource) Sleep(duration time.Duration) {
	time.Sleep(duration)
}

type realSource struct {
	initMutex     sync.Mutex
	isInitialized atomic.Bool
	start         time.Time
	startNanos    atomic.Int64
}

func (c *realSource) Init() {
	if !c.isInitialized.Load() {
		c.initMutex.Lock()
		if !c.isInitialized.Load() {
			now := time.Now()
			c.start = now
			c.startNanos.Store(now.UnixNano())
			c.isInitialized.Store(true)
		}
		c.initMutex.Unlock()
	}
}

func (c *realSource) NowNano() int64 {
	if !c.isInitialized.Load() {
		return 0
	}
	return saturatedAdd(c.startNanos.Load(), time.Since(c.start).Nanoseconds())
}

func (c *realSource) Tick(duration time.Duration) <-chan time.Time {
	return time.Tick(duration)
}

func (c *realSource) Sleep(duration time.Duration) {
	time.Sleep(duration)
}

func saturatedAdd(a, b int64) int64 {
	s := a + b
	if s < a || s < b {
		return math.MaxInt64
	}
	return s
}
