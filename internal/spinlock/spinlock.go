package spinlock

import (
	"runtime"
	"sync/atomic"
)

const maxBackoff = 16

// SpinLock is an implementation of spinlock using exponential backoff algorithm.
type SpinLock uint32

// Lock locks sl. If the lock is already in use, the calling goroutine blocks until the spinlock is available.
func (sl *SpinLock) Lock() {
	backoff := 1
	for !atomic.CompareAndSwapUint32((*uint32)(sl), 0, 1) {
		// Leverage the exponential backoff algorithm, see https://en.wikipedia.org/wiki/Exponential_backoff.
		for i := 0; i < backoff; i++ {
			runtime.Gosched()
		}
		if backoff < maxBackoff {
			backoff <<= 1
		}
	}
}

// Unlock unlocks sl. A locked SpinLock is not associated with a particular goroutine.
// It is allowed for one goroutine to lock a SpinLock and then arrange for another goroutine to unlock it.
func (sl *SpinLock) Unlock() {
	atomic.StoreUint32((*uint32)(sl), 0)
}
