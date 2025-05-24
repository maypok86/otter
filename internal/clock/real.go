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

package clock

import (
	"sync"
	"sync/atomic"
	"time"
)

type Real struct {
	start         time.Time
	initMutex     sync.Mutex
	isInitialized atomic.Bool
	startNanos    atomic.Int64
	cachedOffset  atomic.Int64
}

func (c *Real) Init() {
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

func (c *Real) Refresh() int64 {
	offset := c.Offset()
	c.cachedOffset.Store(offset)
	return offset
}

func (c *Real) Offset() int64 {
	if !c.isInitialized.Load() {
		return 0
	}
	return time.Since(c.start).Nanoseconds()
}

func (c *Real) CachedOffset() int64 {
	return c.cachedOffset.Load()
}

func (c *Real) Nanos(offset int64) int64 {
	return c.startNanos.Load() + offset
}

func (c *Real) Time(offset int64) time.Time {
	return c.start.Add(time.Duration(offset))
}
