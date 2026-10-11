package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maypok86/otter/v2"
	"github.com/maypok86/otter/v2/coalesce"
)

func main() {
	// Create cache with default options
	cache := otter.Must(&otter.Options[int, int]{})

	var calls atomic.Int64 // Count bulk loader invocations

	// Bulk loader function - processes multiple keys at once
	bulkLoader := otter.BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		calls.Add(1)                       // Count loader invocations
		time.Sleep(100 * time.Millisecond) // Simulate expensive bulk operation

		// Generate results (key → key*10)
		result := make(map[int]int, len(keys))
		for _, k := range keys {
			result[k] = k * 10
		}
		return result, nil
	})

	// Batch the keys of concurrent Get calls: while one bulk load runs,
	// the keys that come wait and leave together in the next one.
	loader := coalesce.Must(bulkLoader, nil)

	// 1000 concurrent clients miss 1000 different keys
	goroutines := 1000
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			value, err := cache.Get(ctx, i, loader)
			if err != nil {
				panic("err should be nil")
			}
			if value != i*10 {
				panic("incorrect result")
			}
		}()
	}
	wg.Wait()

	// Validation checks
	fmt.Println("Total loader calls:", calls.Load())
	if calls.Load() > 10 {
		panic("The keys have not been batched")
	}
}
