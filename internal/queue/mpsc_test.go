// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
// Copyright (c) 2023 Andrey Pechkurov
//
// Copyright notice. This code is a fork of tests for xsync.MPMCQueueOf from this file with some changes:
// https://github.com/puzpuzpuz/xsync/blob/main/mpmcqueueof_test.go
//
// Use of this source code is governed by a MIT license that can be found
// at https://github.com/puzpuzpuz/xsync/blob/main/LICENSE

package queue

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestMPSC_Capacity(t *testing.T) {
	const capacity = 10
	q := NewMPSC[int](capacity)
	gotCapacity := q.Capacity()
	if capacity != gotCapacity {
		t.Fatalf("got %d, want %d", gotCapacity, capacity)
	}
}

func TestMPSC_InsertRemove(t *testing.T) {
	const capacity = 10
	q := NewMPSC[int](capacity)
	for i := 0; i < capacity; i++ {
		q.Insert(i)
	}
	for i := 0; i < capacity; i++ {
		if got := q.Remove(); got != i {
			t.Fatalf("got %v, want %d", got, i)
		}
	}
}

func TestMPSC_InsertBlocksOnFull(t *testing.T) {
	q := NewMPSC[string](1)
	q.Insert("foo")

	done := make(chan struct{})
	flag := int32(0)
	go func() {
		q.Insert("bar")
		if atomic.LoadInt32(&flag) == 0 {
			t.Error("insert on full queue didn't wait for remove")
		}
		done <- struct{}{}
	}()

	time.Sleep(50 * time.Millisecond)
	atomic.StoreInt32(&flag, 1)
	if got := q.Remove(); got != "foo" {
		t.Fatalf("got %v, want foo", got)
	}
	<-done
}

func TestMPSC_RemoveBlocksOnEmpty(t *testing.T) {
	q := NewMPSC[string](2)

	done := make(chan struct{})
	flag := int32(0)
	go func() {
		q.Remove()
		if atomic.LoadInt32(&flag) == 0 {
			t.Error("remove on empty queue didn't wait for insert")
		}
		done <- struct{}{}
	}()

	time.Sleep(50 * time.Millisecond)
	atomic.StoreInt32(&flag, 1)
	q.Insert("foobar")
	<-done
}

func testMPSCConcurrent(t *testing.T, parallelism, ops, goroutines int) {
	t.Helper()
	runtime.GOMAXPROCS(parallelism)

	q := NewMPSC[int](goroutines)

	// run producers.
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			for j := n; j < ops; j += goroutines {
				q.Insert(j)
			}
		}(i)
	}

	// run consumer.
	sum := 0
	for j := 0; j < ops; j++ {
		item := q.Remove()
		sum += item
	}

	expectedSum := ops * (ops - 1) / 2
	if sum != expectedSum {
		t.Errorf("calculated sum is wrong. got %d, want %d", sum, expectedSum)
	}
}

func TestMPSC_Concurrent(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(-1))

	tests := []struct {
		parallelism int
		ops         int
		goroutines  int
	}{
		{
			parallelism: 1,
			ops:         10,
			goroutines:  10,
		},
		{
			parallelism: 2,
			ops:         100,
			goroutines:  20,
		},
		{
			parallelism: 4,
			ops:         1000,
			goroutines:  40,
		},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("testConcurrent-%d", tt.parallelism), func(t *testing.T) {
			testMPSCConcurrent(t, tt.parallelism, tt.ops, tt.goroutines)
		})
	}
}
