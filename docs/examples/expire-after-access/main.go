package main

import (
	"time"

	"github.com/maypok86/otter/v2"
)

func main() {
	// Create cache with expiration policy:
	// - Entries expire 1 second after last ACCESS (read or write)
	// - Uses ExpiryAccessing policy (timer resets on both reads and writes)
	// - Must() panics if configuration is invalid
	cache := otter.Must(&otter.Options[int, int]{
		ExpiryCalculator: otter.ExpiryAccessing[int, int](time.Second),
	})

	// Phase 1: Initial creation and active access
	// ----------------------------------------
	// Create entry at T=0
	cache.Set(1, 1) // [Expiration timer starts]

	// First read at T=0 - resets expiration timer (active access)
	if _, ok := cache.GetIfPresent(1); !ok {
		panic("1 should be found") // Should exist
	}

	// Wait 500ms (T=500ms)
	time.Sleep(500 * time.Millisecond)

	// Second read at T=500ms - resets timer again
	if _, ok := cache.GetIfPresent(1); !ok {
		panic("1 should be found") // Should exist
	}

	// Phase 2: Silent read and expiration test
	// ---------------------------------------
	// Wait another 500ms (T=500ms since last access)
	time.Sleep(500 * time.Millisecond)

	// Silent read - doesn't affect expiration or statistics
	// Useful for monitoring without changing cache behavior
	if _, ok := cache.GetEntryQuietly(1); !ok {
		panic("1 should be found") // Still exists (but timer not reset)
	}

	// Wait 500ms more (T=1000ms since last active access)
	time.Sleep(500 * time.Millisecond)

	// Final check at T=1500ms since creation - should be expired now
	// (Last access was at T=500ms, 1000ms have passed)
	if _, ok := cache.GetIfPresent(1); ok {
		panic("1 shouldn't be found") // Should be expired
	}
}
