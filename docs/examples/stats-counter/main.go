package main

import (
	"github.com/maypok86/otter/v2"
	"github.com/maypok86/otter/v2/stats"
)

func main() {
	// Create a new statistics counter
	counter := stats.NewCounter()

	// Initialize cache with statistics recorder
	cache := otter.Must(&otter.Options[int, int]{
		StatsRecorder: counter, // Attach stats collector to cache
	})

	// Phase 1: Populate cache with test data
	// -------------------------------------
	// Insert 10 key-value pairs (0:0 through 9:9)
	for i := 0; i < 10; i++ {
		cache.Set(i, i) // Each Set is recorded in stats
	}

	// Phase 2: Test cache operations
	// -----------------------------
	// Successful gets for existing keys
	for i := 0; i < 10; i++ {
		cache.GetIfPresent(i) // These will count as hits
	}
	// Attempt to get non-existent key
	cache.GetIfPresent(10) // This will count as a miss

	// Phase 3: Verify statistics
	// --------------------------
	// Get atomic snapshot of current statistics
	snapshot := counter.Snapshot()

	// Validate hit count (should match successful gets)
	if snapshot.Hits != 10 {
		panic("incorrect number of hits")
	}
	// Validate miss count (should match failed get)
	if snapshot.Misses != 1 {
		panic("incorrect number of misses")
	}
}
