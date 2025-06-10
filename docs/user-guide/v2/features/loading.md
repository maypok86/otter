---
tags:
  - v2
---

# Loading

Otter provides a `Loader` interface that supports loading values from external sources into the cache when they are not present. This allows you to automatically populate the cache with data as needed.

```go
// Define a loader function
loader := otter.LoaderFunc[string, string](func(ctx context.Context, key string) (string, error) {
	// Load value from external source
	return "loaded value", nil
})

// Get a value, loading it if not present
value, err := cache.Get(context.Background(), "key", loader)
```

If you use `RefreshCalculator`, the cache will try to refresh the stale entries. You can find more detailed examples in the following [chapter](refresh.md).
