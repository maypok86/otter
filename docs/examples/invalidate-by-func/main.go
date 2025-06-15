package main

import "github.com/maypok86/otter/v2"

func main() {
	// Create a new cache with default options
	cache := otter.Must(&otter.Options[int, int]{})

	// Phase 1: Initialize cache with sample data
	// ----------------------------------------
	// Populate cache with keys 0-9 and corresponding values
	for i := 0; i < 10; i++ {
		cache.Set(i, i) // Stores key-value pairs (0:0, 1:1, ..., 9:9)
	}

	// Phase 2: Iterate with concurrent modification
	// --------------------------------------------
	// The All() method returns an iterator over cache entries
	// Otter supports modifications during iteration
	for key := range cache.All() {
		if key%2 == 0 {
			continue // Skip even keys
		}
		// Delete odd keys while iterating!
		cache.Invalidate(key)
	}

	// Phase 3: Verify results
	// -----------------------
	// Check that all odd keys were removed
	for i := 0; i < 10; i++ {
		if _, ok := cache.GetIfPresent(i); ok && i%2 == 1 {
			panic("odd key found")
		}
	}
}
