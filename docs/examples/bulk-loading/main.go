package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maypok86/otter/v2"
)

func main() {
	// Create cache with default options
	cache := otter.Must(&otter.Options[int, int]{})

	var (
		wg    sync.WaitGroup // Synchronize goroutines
		calls atomic.Int64   // Count bulk loader invocations
		total atomic.Int64   // Track total keys requested
	)

	goroutines := 1000           // Number of concurrent clients
	ctx := context.Background()  // Context for operations
	size := 100                  // Number of unique keys
	keys := make([]int, 0, size) // Pre-generate test keys (0-99)
	for i := 0; i < size; i++ {
		keys = append(keys, i)
	}

	// Bulk loader function - processes multiple keys at once
	bulkLoader := otter.BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		total.Add(int64(len(keys))) // Track total keys processed
		calls.Add(1)                // Count loader invocations
		time.Sleep(time.Second)     // Simulate expensive bulk operation

		// Generate results (key → key+100)
		result := make(map[int]int, len(keys))
		for _, k := range keys {
			if k < 0 || k >= size {
				panic("incorrect key") // Validate key range
			}
			result[k] = k + size // Compute value
		}
		return result, nil
	})

	// Simulate cache stampede with 1000 concurrent clients
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			// Each goroutine gets a shuffled copy of keys
			copied := make([]int, size)
			copy(copied, keys)
			rand.Shuffle(len(copied), func(i, j int) {
				copied[i], copied[j] = copied[j], copied[i]
			})

			// Bulk request keys
			result, err := cache.BulkGet(ctx, copied, bulkLoader)
			if err != nil {
				panic("err should be nil")
			}

			// Verify all returned values
			for k, v := range result {
				if v != k+size {
					panic("incorrect result")
				}
			}
		}()
	}
	wg.Wait()

	// Validation checks
	if total.Load() != int64(size) {
		panic("The cache must never load more than 'size' keys")
	}
	fmt.Println("Total loader calls:", calls.Load())
	if calls.Load() > 5 {
		panic("The loader have been called too many times")
	}
}
