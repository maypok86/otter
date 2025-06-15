package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maypok86/otter/v2"
)

func main() {
	// Create a new cache with default options
	cache := otter.Must(&otter.Options[int, int]{})

	key := 1                    // The cache key we'll be testing
	ctx := context.Background() // Context for the cache operation

	// Attempt to get the key with a loader function that returns an error
	value, err := cache.Get(ctx, key, otter.LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
		time.Sleep(200 * time.Millisecond) // Simulate a slow operation
		// Return an error wrapped with otter.ErrNotFound
		return 256, fmt.Errorf("lalala: %w", otter.ErrNotFound)
	}))

	// Validate the returned value and error
	if value != 0 {
		panic("incorrect value") // Should return zero value on error
	}
	if err == nil || !errors.Is(err, otter.ErrNotFound) {
		panic("incorrect err") // Should preserve the ErrNotFound
	}

	// Verify the key wasn't stored in cache due to the error
	if _, ok := cache.GetIfPresent(key); ok {
		panic("1 shouldn't be found") // Failed loads shouldn't cache
	}
}
