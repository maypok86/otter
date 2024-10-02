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
}

func (c *Real) Init() {
	if !c.isInitialized.Load() {
		c.initMutex.Lock()
		if !c.isInitialized.Load() {
			c.start = time.Now()
			c.isInitialized.Store(true)
		}
		c.initMutex.Unlock()
	}
}

func (c *Real) Offset() int64 {
	if !c.isInitialized.Load() {
		return 0
	}
	return time.Since(c.start).Nanoseconds()
}

func (c *Real) Time(offset int64) time.Time {
	return c.start.Add(time.Duration(offset))
}
