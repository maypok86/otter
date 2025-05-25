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

package queue

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const minCapacity = 4

func TestGrowable_PushPop(t *testing.T) {
	t.Parallel()

	const capacity = 10
	g := NewGrowable[int](minCapacity, capacity)
	for i := 0; i < capacity; i++ {
		g.Push(i)
	}
	for i := 0; i < capacity; i++ {
		if got := g.Pop(); got != i {
			t.Fatalf("got %v, want %d", got, i)
		}
	}
}

func TestGrowable_ClearAndPopBlocksOnEmpty(t *testing.T) {
	t.Parallel()

	const capacity = 10
	g := NewGrowable[int](minCapacity, capacity)
	for i := 0; i < capacity; i++ {
		g.Push(i)
	}

	g.clear()

	cdone := make(chan bool)
	flag := int32(0)
	go func() {
		g.Pop()
		if atomic.LoadInt32(&flag) == 0 {
			t.Error("pop on empty queue didn't wait for pop")
		}
		cdone <- true
	}()
	time.Sleep(50 * time.Millisecond)
	atomic.StoreInt32(&flag, 1)
	g.Push(-1)
	<-cdone
}

func TestGrowable_PushBlocksOnFull(t *testing.T) {
	t.Parallel()

	g := NewGrowable[string](1, 1)
	g.Push("foo")

	done := make(chan struct{})
	flag := int32(0)
	go func() {
		g.Push("bar")
		if atomic.LoadInt32(&flag) == 0 {
			t.Error("push on full queue didn't wait for pop")
		}
		done <- struct{}{}
	}()

	time.Sleep(50 * time.Millisecond)
	atomic.StoreInt32(&flag, 1)
	if got := g.Pop(); got != "foo" {
		t.Fatalf("got %v, want foo", got)
	}
	<-done
}

func TestGrowable_PopBlocksOnEmpty(t *testing.T) {
	t.Parallel()

	g := NewGrowable[string](2, 2)

	done := make(chan struct{})
	flag := int32(0)
	go func() {
		g.Pop()
		if atomic.LoadInt32(&flag) == 0 {
			t.Error("pop on empty queue didn't wait for push")
		}
		done <- struct{}{}
	}()

	time.Sleep(50 * time.Millisecond)
	atomic.StoreInt32(&flag, 1)
	g.Push("foobar")
	<-done
}

func testGrowableConcurrent(t *testing.T, parallelism, ops, goroutines int) {
	t.Helper()
	runtime.GOMAXPROCS(parallelism)

	g := NewGrowable[int](minCapacity, uint32(goroutines))
	var wg sync.WaitGroup
	wg.Add(1)
	csum := make(chan int, goroutines)

	// run producers.
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			wg.Wait()
			for j := n; j < ops; j += goroutines {
				g.Push(j)
			}
		}(i)
	}

	// run consumers.
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			wg.Wait()
			sum := 0
			for j := n; j < ops; j += goroutines {
				item := g.Pop()
				sum += item
			}
			csum <- sum
		}(i)
	}
	wg.Done()
	// Wait for all the sums from producers.
	sum := 0
	for i := 0; i < goroutines; i++ {
		s := <-csum
		sum += s
	}

	expectedSum := ops * (ops - 1) / 2
	if sum != expectedSum {
		t.Errorf("calculated sum is wrong. got %d, want %d", sum, expectedSum)
	}
}

func TestGrowable_Concurrent(t *testing.T) {
	t.Parallel()

	t.Cleanup(func() {
		runtime.GOMAXPROCS(runtime.GOMAXPROCS(-1))
	})

	n := 100
	if testing.Short() {
		n = 10
	}

	tests := []struct {
		parallelism int
		ops         int
		goroutines  int
	}{
		{
			parallelism: 1,
			ops:         100 * n,
			goroutines:  n,
		},
		{
			parallelism: 1,
			ops:         1000 * n,
			goroutines:  10 * n,
		},
		{
			parallelism: 4,
			ops:         100 * n,
			goroutines:  n,
		},
		{
			parallelism: 4,
			ops:         1000 * n,
			goroutines:  10 * n,
		},
		{
			parallelism: 8,
			ops:         100 * n,
			goroutines:  n,
		},
		{
			parallelism: 8,
			ops:         1000 * n,
			goroutines:  10 * n,
		},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("testConcurrent-%d-%d", tt.parallelism, tt.ops), func(t *testing.T) {
			t.Parallel()
			testGrowableConcurrent(t, tt.parallelism, tt.ops, tt.goroutines)
		})
	}
}
