// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
// Copyright 2009 The Go Authors. All rights reserved.
//
// Copyright notice. Initial version of the following tests was based on
// the following file from the Go Programming Language core repo:
// https://github.com/golang/go/blob/831f9376d8d730b16fb33dfd775618dffe13ce7a/src/sync/mutex_test.go
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// That can be found at https://github.com/golang/go/blob/831f9376d8d730b16fb33dfd775618dffe13ce7a/LICENSE

//go:build race

package node

import (
	"runtime"
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
