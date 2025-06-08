---
tags:
  - v1
---

# Statistics

By using `CollectStats` in the builder, you can turn on statistics collection.
The `Stats` function in the cache returns a `Stats` struct which provides statistics such as

- `Hits` returns the number of cache hits.
- `Misses` returns the number of cache misses.
- `Ratio` returns the cache hit ratio.

The cache statistics can be integrated with a reporting system using a pull approach.
A pull-based approach periodically calls `Stats` function and records the latest snapshot.

??? example
    ```go
    cache, err := otter.MustBuilder[string, string](10_000).
        CollectStats().
        Build()
    if err != nil {
        panic(err)
    }
    
    // ... awesome code ...
    
    stats := cache.Stats()
    hits := stats.Hits()
    misses := stats.Misses()
    ratio := stats.Ratio()
    
    // record latest snapshot
    ```
