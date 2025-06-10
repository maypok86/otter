---
tags:
  - v2
---

# Statistics

```go
cache := otter.Must(&otter.Options[string, string]{
	StatsRecorder: stats.NewCounter(),
})
```

By using `Options.StatsRecorder`, you can turn on statistics collection.

You can use `stats.Counter` as an implementation of `stats.Recorder` interface. `stats.Counter` also has a `Snapshot` method that returns `stats.Stats` which provides statistics such as

- `HitRatio()`: returns the ratio of hits to requests
- `Evictions`: the number of cache evictions
- `AverageLoadPenalty()`: the average time spent loading new values

These statistics are critical in cache tuning, and we advise keeping an eye on these statistics in performance-critical applications.

The cache statistics can be integrated with a reporting system using either a pull or push based approach. A pull-based approach periodically gets the latest snapshot and records it. A push-based approach supplies a custom `stats.Recorder` so that the metrics are updated directly during the cache operations.
