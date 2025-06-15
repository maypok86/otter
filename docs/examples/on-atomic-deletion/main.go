package main

import (
	"sync"

	"github.com/maypok86/otter/v2"
)

func main() {
	// Synchronization for concurrent access to eviction map
	var mutex sync.Mutex

	// Cache configuration
	maximumSize := 10
	// Map to track eviction reasons
	m := make(map[otter.DeletionCause]int)

	// Create cache with eviction callback
	cache := otter.Must[int, int](&otter.Options[int, int]{
		MaximumSize: maximumSize, // Cache capacity
		OnAtomicDeletion: func(e otter.DeletionEvent[int, int]) {
			// Only count evictions (not explicit deletions)
			if e.WasEvicted() {
				mutex.Lock()
				m[e.Cause]++ // Increment counter for this eviction cause
				mutex.Unlock()
			}
		},
	})

	// Phase 1: Fill cache to capacity
	// ------------------------------
	// Add entries 0-9 (total of 10)
	for i := 0; i < maximumSize; i++ {
		cache.Set(i, i)
	}

	// Phase 2: Force eviction of all entries
	// -------------------------------------
	// Reduce cache size to 0, forcing eviction of all entries
	cache.SetMaximum(0) // Triggers OnAtomicDeletion for each evicted entry

	// Phase 3: Validate eviction tracking
	// -----------------------------------
	// Verify we recorded exactly 10 overflow evictions
	if len(m) != 1 || m[otter.CauseOverflow] != maximumSize {
		panic("invalid OnAtomicDeletion call detected")
	}
}
