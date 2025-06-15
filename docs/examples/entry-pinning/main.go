package main

import (
	"github.com/maypok86/otter/v2"
)

func main() {
	// Define cache configuration parameters
	maximumSize := 10 // Maximum number of regular (non-pinned) items
	pinnedKey := 4    // Special key that will be pinned (never evicted)

	// Initialize cache with weight-based eviction
	cache := otter.Must[int, int](&otter.Options[int, int]{
		MaximumWeight: uint64(maximumSize), // Total weight capacity

		// Custom weigher function determines entry weights
		Weigher: func(key int, value int) uint32 {
			if key == pinnedKey {
				return 0 // Pinned entry has 0 weight (never counts against capacity)
			}
			return 1 // All other entries have weight 1
		},
	})

	// Populate cache with test data
	for i := 0; i < maximumSize; i++ {
		cache.Set(i, i) // Add entries with keys 0-9
	}

	// Force eviction of all entries that can be evicted
	// Setting maximum to 0 will remove all entries with weight > 0
	cache.SetMaximum(0)

	// Verify eviction behavior
	if _, ok := cache.GetIfPresent(0); ok {
		panic("0 shouldn't be found") // Regular entry should be evicted
	}
	if _, ok := cache.GetIfPresent(pinnedKey); !ok {
		panic("4 should be found") // Pinned entry should remain
	}
}
