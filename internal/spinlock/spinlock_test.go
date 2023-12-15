// Copyright notice. Initial version of the following tests was based on
// the following file from the Go Programming Language core repo:
// https://github.com/golang/go/blob/831f9376d8d730b16fb33dfd775618dffe13ce7a/src/sync/mutex_test.go
package spinlock

import (
	"runtime"
	"testing"
	"time"
)

func HammerSpinLock(sl *SpinLock, loops int, cdone chan bool) {
	for i := 0; i < loops; i++ {
		sl.Lock()
		sl.Unlock()
	}
	cdone <- true
}

func TestSpinLock(t *testing.T) {
	if n := runtime.SetMutexProfileFraction(1); n != 0 {
		t.Logf("got mutexrate %d expected 0", n)
	}
	defer runtime.SetMutexProfileFraction(0)
	sl := new(SpinLock)
	c := make(chan bool)
	for i := 0; i < 10; i++ {
		go HammerSpinLock(sl, 1000, c)
	}
	for i := 0; i < 10; i++ {
		<-c
	}
}

func TestSpinLockFairness(t *testing.T) {
	var sl SpinLock
	stop := make(chan bool)
	defer close(stop)
	go func() {
		for {
			sl.Lock()
			time.Sleep(100 * time.Microsecond)
			sl.Unlock()
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	done := make(chan bool, 1)
	go func() {
		for i := 0; i < 10; i++ {
			time.Sleep(100 * time.Microsecond)
			sl.Lock()
			sl.Unlock()
		}
		done <- true
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("can't acquire SpinLock in 10 seconds")
	}
}
