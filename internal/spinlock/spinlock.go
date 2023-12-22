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
