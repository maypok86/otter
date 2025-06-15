package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maypok86/otter/v2"
)

func main() {
	// Create basic cache with default configuration
	cache := otter.Must(&otter.Options[int, int]{})

	var (
		wg    sync.WaitGroup // WaitGroup to synchronize goroutines
		calls atomic.Int64   // Atomic counter to track loader calls
	)

	// Test parameters
	goroutines := 1000          // Number of concurrent requests to simulate
	ctx := context.Background() // Context for cache operations
	key := 15                   // Cache key to test
	value := key + 10000        // Expected value (15 + 10000 = 10015)

	// Define loader function that will be called on cache misses
	loader := otter.LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
		calls.Add(1)            // Increment call counter (atomic)
		time.Sleep(time.Second) // Simulate expensive operation
		return value, nil       // Return the computed value
	})

	// Cache stampede simulation - 1000 concurrent requests for same key
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			// All goroutines try to get the same key simultaneously
			// Otter will deduplicate loader calls automatically
			v, err := cache.Get(ctx, key, loader)
			if err != nil {
				panic("err should be nil")
			}
			if v != key+10000 {
				panic("incorrect value")
			}
		}()
	}
	wg.Wait() // Wait for all goroutines to complete

	// Verify loader was called exactly once despite 1000 concurrent requests
	if calls.Load() != 1 {
		panic("The loader should have been called only once")
	}
}
