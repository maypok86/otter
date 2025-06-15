package main

import (
	"time"

	"github.com/maypok86/otter/v2"
)

func main() {
	// Create cache with custom configuration:
	// - Maximum weight capacity: 10
	// - Custom weigher function (9 * key)
	// - Default expiration: 1000 days (but will be overridden)
	cache := otter.Must[int, int](&otter.Options[int, int]{
		MaximumWeight: 10,
		Weigher: func(key int, value int) uint32 {
			return 9 * uint32(key) // Weight calculation (key=1 → weight=9)
		},
		ExpiryCalculator: otter.ExpiryWriting[int, int](1000 * 24 * time.Hour),
	})

	key := 1 // Test key

	// Phase 1: Test expiration override
	// --------------------------------
	// Add entry with default expiration (1000 days)
	cache.Set(key, key)
	// Override expiration to 1 second
	cache.SetExpiresAfter(key, time.Second)

	// Wait for expiration
	time.Sleep(time.Second)

	// Verify entry expired
	if _, ok := cache.GetIfPresent(key); ok {
		panic("1 shouldn't be found") // Should be expired
	}

	// Phase 2: Test weight calculation
	// --------------------------------
	// Re-add the entry
	cache.Set(key, key)

	// Verify entry properties
	if entry, ok := cache.GetEntry(key); !ok || entry.Weight != 9 {
		panic("incorrect entry") // Should have weight=9 (9*1)
	}
	// Check total cache weight
	if cache.WeightedSize() != 9 {
		panic("incorrect weightedSize")
	}

	// Phase 3: Test dynamic resizing
	// -----------------------------
	// Reduce maximum weight to 5 (current weight=9 exceeds this)
	cache.SetMaximum(5) // Triggers immediate eviction

	// Verify entry was evicted due to size constraint
	if _, ok := cache.GetIfPresent(key); ok {
		panic("1 shouldn't be found")
	}
	// Verify cache is now empty
	if cache.WeightedSize() != 0 {
		panic("incorrect weightedSize")
	}
	// Verify new maximum was set
	if cache.GetMaximum() != 5 {
		panic("incorrect maximum")
	}
}
