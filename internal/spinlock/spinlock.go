// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
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

package spinlock

import (
	"runtime"
	"sync/atomic"
)

const maxSpins = 16

// SpinLock is an implementation of spinlock.
type SpinLock uint32

// Lock locks sl. If the lock is already in use, the calling goroutine blocks until the spinlock is available.
func (sl *SpinLock) Lock() {
	spins := 0
	for {
		for atomic.LoadUint32((*uint32)(sl)) == 1 {
			spins++
			if spins > maxSpins {
				spins = 0
				runtime.Gosched()
			}
		}

		if atomic.CompareAndSwapUint32((*uint32)(sl), 0, 1) {
			return
		}

		spins = 0
	}
}

// Unlock unlocks sl. A locked SpinLock is not associated with a particular goroutine.
// It is allowed for one goroutine to lock a SpinLock and then arrange for another goroutine to unlock it.
func (sl *SpinLock) Unlock() {
	atomic.StoreUint32((*uint32)(sl), 0)
}
