package main

import (
	"time"

	"github.com/maypok86/otter/v2"
)

func main() {
	// Create a cache with expiration time of 1 second after creation
	// - Uses ExpiryCreating policy (timer starts at creation)
	// - Must() will panic if configuration is invalid
	cache := otter.Must(&otter.Options[int, int]{
		ExpiryCalculator: otter.ExpiryCreating[int, int](time.Second),
	})

	// Test Phase 1: Initial creation and first expiration check
	// --------------------------------------------------------
	// Add entry with key=1, value=1 at time T=0
	cache.Set(1, 1)

	// Immediate check - should exist (not expired yet)
	if _, ok := cache.GetIfPresent(1); !ok {
		panic("1 should be found") // Entry should exist
	}

	// Wait 500ms (half the expiration duration)
	time.Sleep(500 * time.Millisecond) // Now at T=500ms

	// Check again - should still exist (500ms < 1s)
	if _, ok := cache.GetIfPresent(1); !ok {
		panic("1 should be found") // Entry should persist
	}

	// Test Phase 2: Update and final expiration check
	// ----------------------------------------------
	// Update the value at T=500ms - this DOESN'T RESET the expiration timer
	cache.Set(1, 2)

	// Wait another 500ms (total 1s since creation, 0.5s since update)
	time.Sleep(500 * time.Millisecond) // Now at T=1000ms since creation

	// Final check - should be expired now (1s since creation)
	if _, ok := cache.GetIfPresent(1); ok {
		panic("1 shouldn't be found") // Entry should be expired
	}
}
