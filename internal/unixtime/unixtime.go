package unixtime

import (
	"sync"
	"sync/atomic"
	"time"
)

// We need this package because time.Now() is slower, allocates memory,
// and we don't need a more precise time for the expiry time (and most other operations).
var now uint64

var (
	mutex         sync.Mutex
	countInstance int
	done          chan struct{}
)

func startTimer() {
	done = make(chan struct{})
	atomic.StoreUint64(&now, uint64(time.Now().Unix()))

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case t := <-ticker.C:
				atomic.StoreUint64(&now, uint64(t.Unix()))
			case <-done:
				return
			}
		}
	}()
}

// Start should be called when the cache instance is created to initialize the timer.
func Start() {
	mutex.Lock()
	defer mutex.Unlock()

	if countInstance == 0 {
		startTimer()
	}

	countInstance++
}

// Stop should be called when closing and stopping the cache instance to stop the timer.
func Stop() {
	mutex.Lock()
	defer mutex.Unlock()

	countInstance--
	if countInstance == 0 {
		done <- struct{}{}
		close(done)
	}
}

// Now returns time as a Unix time, the number of seconds elapsed since January 1, 1970 UTC.
func Now() uint64 {
	return atomic.LoadUint64(&now)
}
