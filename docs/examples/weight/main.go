package main

import "github.com/maypok86/otter/v2"

func main() {
	// Initialize cache with panic on error (Must wraps New and panics if config is invalid)
	cache := otter.Must(&otter.Options[int, int]{
		MaximumWeight: 5, // Total weight capacity of the cache
		// Weigher defines how to calculate weight of each entry
		// In this case, we use the key itself as the weight value
		// Cache will enforce: sum(weights) <= MaximumWeight
		Weigher: func(key int, value int) uint32 {
			return uint32(key) // Convert key to unsigned 32-bit weight
		},
	})

	// Define test keys
	k1 := 3 // Will be kept (weight 3)
	k2 := 4 // Will be evicted (weight 3 + 4 = 7 > 5)

	// Store values in cache
	cache.Set(k1, k1) // Adds entry with weight = 3
	cache.Set(k2, k2) // Adds entry with weight = 4 (total weight now exceeds limit)

	// Force immediate processing of pending operations
	// This applies eviction policy synchronously
	cache.CleanUp()

	// Verify cache state after eviction
	if _, ok := cache.GetIfPresent(k1); !ok {
		panic("not found k1")
	}
	if _, ok := cache.GetIfPresent(k2); ok {
		panic("found k2") // Should be evicted due to weight limit
	}
}
