package main

import (
	"context"
	"time"

	"github.com/maypok86/otter/v2"
)

func main() {
	// Create cache with refresh policy:
	// - Entries refresh 500ms after last WRITE (creation/update)
	// - Uses RefreshWriting policy (timer resets on writes)
	cache := otter.Must(&otter.Options[int, int]{
		RefreshCalculator: otter.RefreshWriting[int, int](500 * time.Millisecond),
	})

	key := 1 // Test key
	ctx := context.Background()

	// Phase 1: Initial setup
	// ----------------------
	// Set initial value at T=0
	cache.Set(key, key) // [Refresh timer starts]

	// Wait until refresh should trigger (500ms)
	time.Sleep(500 * time.Millisecond) // T=500ms

	// Phase 2: Refresh handling
	// ------------------------
	// Define loader that will be called during refresh
	loader := otter.LoaderFunc[int, int](func(ctx context.Context, k int) (int, error) {
		if k != key {
			panic("unexpected key") // Safety check
		}
		time.Sleep(200 * time.Millisecond) // Simulate slow refresh
		return key + 1, nil                // Return new value
	})

	// This Get operation occurs at T=500ms when refresh is due
	// - Returns current value (1) immediately
	// - Triggers async refresh in background
	value, err := cache.Get(ctx, key, loader)
	if err != nil {
		panic(err)
	}
	if value != key { // Should get old value while refreshing
		panic("loader shouldn't be called during Get")
	}

	// Phase 3: Verify refresh completion
	// ---------------------------------
	// Wait for refresh to complete (200ms + buffer)
	time.Sleep(210 * time.Millisecond) // T=710ms

	// Verify new value is now in cache
	v, ok := cache.GetIfPresent(key)
	if !ok {
		panic("key should be found")
	}
	if v != key+1 { // Should see refreshed value
		panic("refresh should be completed")
	}
}
