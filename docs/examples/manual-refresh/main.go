package main

import (
	"context"
	"time"

	"github.com/maypok86/otter/v2"
)

func main() {
	// Create cache with refresh policy:
	// - Entries marked for refresh 500ms after last write
	// - Uses RefreshWriting policy (timer based on writes, not reads)
	cache := otter.Must(&otter.Options[int, int]{
		RefreshCalculator: otter.RefreshWriting[int, int](500 * time.Millisecond),
	})

	key := 1 // Test key
	ctx := context.Background()

	// Phase 1: Initial setup
	// ----------------------
	// Set initial value at T=0
	cache.Set(key, key) // [Refresh timer starts]

	// Wait 200ms (before automatic refresh would trigger)
	time.Sleep(200 * time.Millisecond) // T=200ms

	// Phase 2: Manual refresh
	// ----------------------
	// Define loader for refresh operation
	loader := otter.LoaderFunc[int, int](func(ctx context.Context, k int) (int, error) {
		if k != key {
			panic("unexpected key") // Validate key
		}
		time.Sleep(200 * time.Millisecond) // Simulate refresh delay
		return key + 1, nil                // Return new value
	})

	// Explicitly trigger refresh before automatic timeout
	// Returns channel that will receive refresh result
	done := cache.Refresh(ctx, key, loader) // T=200ms

	// Phase 3: Verify behavior during refresh
	// --------------------------------------
	// Check cache state while refresh is in progress
	v, ok := cache.GetIfPresent(key)
	if !ok {
		panic("key should be found") // Should still be available
	}
	if v != key { // Should still show old value during refresh
		panic("incorrect value")
	}

	// Phase 4: Verify refresh completion
	// ---------------------------------
	// Wait for refresh to complete (blocks until done)
	result := <-done // T=400ms

	// Verify new value is in cache
	v, ok = cache.GetIfPresent(key)
	if !ok {
		panic("key should be found") // Should persist after refresh
	}
	// Should match both:
	// - Direct cache check
	// - Result from refresh operation
	if v != key+1 || v != result.Value {
		panic("incorrect value")
	}
}
