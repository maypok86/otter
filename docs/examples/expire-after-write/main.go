package main

import (
	"time"

	"github.com/maypok86/otter/v2"
)

func main() {
	// Create cache with expiration policy:
	// - Entries expire 1 second after last WRITE (creation/update)
	// - Uses ExpiryWriting policy (vs ExpiryCreating which counts from first creation)
	// - Must() panics if configuration is invalid
	cache := otter.Must(&otter.Options[int, int]{
		ExpiryCalculator: otter.ExpiryWriting[int, int](time.Second),
	})

	// Phase 1: Initial creation and silent read
	// ----------------------------------------
	// Create entry at T=0
	cache.Set(1, 1) // [Expiration timer starts]

	// Normal read - updates access metadata
	if _, ok := cache.GetIfPresent(1); !ok {
		panic("1 should be found") // Should exist
	}

	// Wait 500ms (T=500ms)
	time.Sleep(500 * time.Millisecond)

	// Silent read - doesn't affect expiration or statistics
	// Useful for monitoring without changing cache behavior
	if _, ok := cache.GetEntryQuietly(1); !ok {
		panic("1 should be found") // Should still exist
	}

	// Phase 2: Update and expiration validation
	// ----------------------------------------
	// Update at T=500ms - RESETS expiration timer
	cache.Set(1, 2) // [Timer restarts]

	// Wait 500ms (T=1000ms since creation)
	time.Sleep(500 * time.Millisecond)

	// Should still exist (500ms since update < 1s)
	if _, ok := cache.GetIfPresent(1); !ok {
		panic("1 should be found") // Should still exist
	}

	// Wait another 500ms (T=1500ms since creation, T=1000ms since last update)
	time.Sleep(500 * time.Millisecond)

	// Should now be expired (1000ms >= 1000ms since last write)
	if _, ok := cache.GetIfPresent(1); ok {
		panic("1 shouldn't be found") // Expired
	}
}
