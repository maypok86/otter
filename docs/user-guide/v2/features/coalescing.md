---
tags:
  - v2
---

# Coalescing

When many goroutines miss different keys at the same time, each miss reaches the data source with a request of its own. `coalesce.Loader` gathers these keys and loads them with one call of an `otter.BulkLoader`.

```go
// Define a bulk loader
bulkLoader := otter.BulkLoaderFunc[string, string](func(ctx context.Context, keys []string) (map[string]string, error) {
	// Load all keys from external source with one request
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = "loaded value for " + key
	}
	return result, nil
})

// Batch the loads of concurrent calls
loader := coalesce.Must(bulkLoader, &coalesce.Options{
	MaxBatchSize: 100,
})

value, err := cache.Get(context.Background(), "key", loader)
```

`coalesce.Loader` implements both `otter.Loader` and `otter.BulkLoader`, so the same loader works with `Get`, `BulkGet`, `Refresh` and `BulkRefresh`: the keys of `Get` and `BulkGet` share batches, and so do the keys of `Refresh` and `BulkRefresh`. A key that the bulk loader leaves out of its result is reported as `otter.ErrNotFound`.

A batch runs with the context values of its first call, such as tracing data. Do not make the loaded values depend on the caller, for example on credentials in the context.

You can tune batching with `coalesce.Options`:

- `MaxBatchSize`: the most keys in one call of the bulk loader. Zero means no limit.
- `MaxInFlight`: how many calls of the bulk loader run at once. Zero means one, and `-1` means no limit. While every call is busy, new keys wait and leave together in the next batch.
- `MaxDelay`: how long a batch waits for more keys after its first key comes. It adds up to `MaxDelay` to a load, so it is off by default.
