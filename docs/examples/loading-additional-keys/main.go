package main

import (
	"context"
	"time"

	"github.com/maypok86/otter/v2"
)

func main() {
	// Create cache with default configuration
	cache := otter.Must(&otter.Options[int, int]{})

	ctx := context.Background()
	size := 100         // Value offset
	keys := []int{0, 1} // Initial keys to request
	additionalKey := 2  // Extra key that loader will add

	// Bulk loader function that simulates a backend operation
	bulkLoader := otter.BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
		time.Sleep(200 * time.Millisecond) // Simulate processing delay

		// Create result map with requested keys
		result := make(map[int]int, len(keys))
		for _, k := range keys {
			result[k] = k + size // Store value as key + offset
		}

		// Add an extra key-value pair that wasn't requested
		result[additionalKey] = additionalKey + size
		return result, nil
	})

	// Perform bulk get operation for initial keys
	result, err := cache.BulkGet(ctx, keys, bulkLoader)
	if err != nil {
		panic("err should be nil") // Should never error in this case
	}

	// Verify all returned values are correct
	for k, v := range result {
		if v != k+size {
			panic("incorrect value") // Validate value calculation
		}
		// Check if each key is now cached
		if _, ok := cache.GetIfPresent(k); !ok {
			panic("key should be found") // Requested keys should be cached
		}
	}

	// Verify the additional key was also cached
	if _, ok := cache.GetIfPresent(additionalKey); !ok {
		panic("key should be found") // Extra key should be cached too
	}
}
