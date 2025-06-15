package main

import (
	"context"
	"sync"
	"time"

	"github.com/maypok86/otter/v2"
)

func main() {
	// Create cache with default options
	cache := otter.Must[int, int](&otter.Options[int, int]{})

	key := 10          // Test key
	value := key + 100 // Expected value (110)
	ctx := context.Background()

	// Communication channels for controlling test flow:
	done := make(chan struct{}) // Signals when loader starts
	inv := make(chan struct{})  // Controls loader completion

	// Loader function that simulates slow backend operation
	loader := otter.LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
		done <- struct{}{}                 // Signal that loader has started
		time.Sleep(200 * time.Millisecond) // Simulate slow operation
		<-inv                              // Wait for invalidation signal
		return value, nil                  // Return the computed value
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()

		// First goroutine tries to get the value
		v, err := cache.Get(ctx, key, loader)
		if err != nil {
			panic("err should be nil")
		}
		if v != value {
			panic("incorrect value")
		}
	}()

	// Wait for loader to start (blocks until done receives signal)
	<-done

	// Concurrent operations:
	// 1. Invalidate the key while it's being loaded
	cache.Invalidate(key)

	// 2. Allow loader to complete
	inv <- struct{}{}

	// Wait for Get operation to complete
	wg.Wait()

	// Verify the key was properly invalidated and not cached
	if _, ok := cache.GetIfPresent(key); ok {
		panic("key shouldn't be found")
	}
}
