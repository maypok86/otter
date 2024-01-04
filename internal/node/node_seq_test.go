// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
// Copyright 2009 The Go Authors. All rights reserved.
//
// Copyright notice. Initial version of the following tests was based on
// the following file from the Go Programming Language core repo:
// https://github.com/golang/go/blob/831f9376d8d730b16fb33dfd775618dffe13ce7a/src/sync/rwmutex_test.go
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// That can be found at https://github.com/golang/go/blob/831f9376d8d730b16fb33dfd775618dffe13ce7a/LICENSE

//go:build !race

package node

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func HammerNodeLock(n *Node[int, int], loops int, cdone chan bool) {
	for i := 0; i < loops; i++ {
		n.Lock()
		n.Unlock()
	}
	cdone <- true
}

func TestNodeLock(t *testing.T) {
	if n := runtime.SetMutexProfileFraction(1); n != 0 {
		t.Logf("got mutexrate %d expected 0", n)
	}
	defer runtime.SetMutexProfileFraction(0)
	n := &Node[int, int]{}
	c := make(chan bool)
	for i := 0; i < 10; i++ {
		go HammerNodeLock(n, 1000, c)
	}
	for i := 0; i < 10; i++ {
		<-c
	}
}

func TestNodeLockFairness(t *testing.T) {
	n := &Node[int, int]{}
	stop := make(chan bool)
	defer close(stop)
	go func() {
		for {
			n.Lock()
			time.Sleep(100 * time.Microsecond)
			n.Unlock()
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
			n.Lock()
			n.Unlock()
		}
		done <- true
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("can't acquire SpinLock in 10 seconds")
	}
}

type triple struct {
	a int
	b int
	c int
}

func TestNodeSeqLock(t *testing.T) {
	const (
		goroutines         = 100
		numberOfIterations = 1000000
	)

	n := New[int, triple](-1, triple{}, 0, 1)
	var ready atomic.Int64
	for i := 0; i < goroutines; i++ {
		go func() {
			for ready.Load() == 0 {
				runtime.Gosched()
			}

			for j := 0; j < numberOfIterations; j++ {
				v := n.Value()
				if v.a+100 != v.b || v.c != v.a+v.b {
					t.Errorf("not valid value state. got: %+v", v)
				}
			}

			ready.Add(int64(-1))
		}()
	}

	counter := 0
	for {
		n.Lock()
		n.SetValue(triple{
			a: counter,
			b: counter + 100,
			c: 2*counter + 100,
		})
		n.Unlock()
		counter++
		if counter == 1 {
			ready.Add(int64(goroutines))
		}
		if ready.Load() == 0 {
			break
		}
	}

	t.Logf("counter: %d", counter)
}
