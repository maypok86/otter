---
tags:
  - v2
---

# Bulk operations

Otter provides support for bulk operations, allowing you to efficiently perform multiple cache operations at once. This is particularly useful when you need to load or refresh multiple values.

## Bulk loading

```go
// Define a bulk loader
bulkLoader := otter.BulkLoaderFunc[string, string](func(ctx context.Context, keys []string) (map[string]string, error) {
	// Load values from external source
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = "loaded value for " + key
	}
	return result, nil
})
    
// Get values from cache or load them
values, err := cache.BulkGet(context.Background(), []string{"key1", "key2"}, bulkLoader)
```

If you use `RefreshCalculator`, the cache will try to refresh the stale entries. You can find more detailed examples in the following chapters ([loading](loading.md), [refresh](refresh.md)). 

## Bulk Refresh

You can use the `BulkRefresh` method to refresh multiple values in the cache asynchronously.

```go
// Define a bulk loader
bulkLoader := otter.BulkLoaderFunc[string, string](func(ctx context.Context, keys []string) (map[string]string, error) {
	// Load values from external source
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = "refreshed value for " + key
	}
	return result, nil
})
    
// Refresh values in the cache
cache.BulkRefresh([]string{"key1", "key2"}, bulkLoader)
```
