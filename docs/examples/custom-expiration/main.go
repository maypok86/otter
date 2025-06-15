package main

import (
	"time"

	"github.com/maypok86/otter/v2"
)

// Custom expiration policy implementation
type expiryCalculator struct{}

// ExpireAfterCreate sets expiration time for new entries (500ms)
func (ec *expiryCalculator) ExpireAfterCreate(_ otter.Entry[int, int]) time.Duration {
	return 500 * time.Millisecond
}

// ExpireAfterUpdate sets expiration time after updates (300ms)
func (ec *expiryCalculator) ExpireAfterUpdate(_ otter.Entry[int, int], _ int) time.Duration {
	return 300 * time.Millisecond
}

// ExpireAfterRead returns remaining expiration time for reads
func (ec *expiryCalculator) ExpireAfterRead(entry otter.Entry[int, int]) time.Duration {
	return entry.ExpiresAfter() // Preserves current expiration
}

func main() {
	// Create cache with our custom expiration policy
	cache := otter.Must(&otter.Options[int, int]{
		ExpiryCalculator: &expiryCalculator{},
	})

	// Phase 1: Entry creation with 500ms expiration
	// --------------------------------------------
	cache.Set(1, 1) // New entry - expires in 500ms

	// Immediate check - should exist
	if _, ok := cache.GetIfPresent(1); !ok {
		panic("1 should be found")
	}

	// Wait 490ms (just before initial expiration)
	time.Sleep(490 * time.Millisecond)

	// Should still exist (490ms < 500ms)
	if _, ok := cache.GetIfPresent(1); !ok {
		panic("1 should be found")
	}

	// Phase 2: Entry update with 300ms expiration
	// -------------------------------------------
	cache.Set(1, 2) // Update - now expires in 300ms

	// Wait 200ms (200ms < 300ms)
	time.Sleep(200 * time.Millisecond)

	// Should still exist (200ms < 300ms)
	if _, ok := cache.GetIfPresent(1); !ok {
		panic("1 should be found")
	}

	// Wait additional 100ms (total 300ms since update)
	time.Sleep(100 * time.Millisecond)

	// Should now be expired (300ms >= 300ms)
	if _, ok := cache.GetIfPresent(1); ok {
		panic("1 shouldn't be found")
	}
}
