package s3fifo

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

func TestQueue_TryInsertRemove(t *testing.T) {
	const capacity = 10
	q := newQueue[int](capacity)
	for i := 0; i < capacity; i++ {
		if !q.tryInsert(i) {
			t.Fatalf("failed to insert value: %d", i)
		}
	}
	for i := 0; i < capacity; i++ {
		if got, ok := q.tryRemove(); !ok || got != i {
			t.Fatalf("got %v, want %d, detected status %v", got, i, ok)
		}
	}
}

func TestQueue_TryInsertInFull(t *testing.T) {
	q := newQueue[string](1)
	if !q.tryInsert("foo") {
		t.Error("failed to insert item")
	}
	if q.tryInsert("bar") {
		t.Error("got success for insert in full queue")
	}
}

func TestQueue_TryRemoveOnEmpty(t *testing.T) {
	q := newQueue[int](4)
	if _, ok := q.tryRemove(); ok {
		t.Error("got success for remove from empty queue")
	}
}

func testQueueConcurrent(t *testing.T, parallelism, ops, goroutines int) {
	t.Helper()
	runtime.GOMAXPROCS(parallelism)

	q := newQueue[int](goroutines)
	wg := sync.WaitGroup{}
	wg.Add(1)
	sumChan := make(chan int, goroutines)

	// run producers.
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			wg.Wait()
			for j := n; j < ops; j += goroutines {
				for !q.tryInsert(j) {
					// busy.
				}
			}
		}(i)
	}

	// run consumers.
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			wg.Wait()
			sum := 0
			for j := n; j < ops; j += goroutines {
				for {
					if item, ok := q.tryRemove(); ok {
						sum += item
						break
					}
					// busy.
				}
			}
			sumChan <- sum
		}(i)
	}

	wg.Done()

	sum := 0
	for i := 0; i < goroutines; i++ {
		s := <-sumChan
		sum += s
	}

	expectedSum := ops * (ops - 1) / 2
	if sum != expectedSum {
		t.Errorf("calculated sum is wrong. got %d, want %d", sum, expectedSum)
	}
}

func TestQueue_Concurrent(t *testing.T) {
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
			testQueueConcurrent(t, tt.parallelism, tt.ops, tt.goroutines)
		})
	}
}
