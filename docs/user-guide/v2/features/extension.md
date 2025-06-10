---
tags:
  - v2
---

# Extension

The cache also has a set of additional methods.

If the cache is bounded by a maximum weight, the current weight can be obtained using `WeightedSize`. This differs from `EstimatedSize` which reports the number of entries present.

```go
cache.SetMaximum(2 * cache.GetMaximum())
```

The maximum size or weight can be read from `GetMaximum` and adjusted using `SetMaximum`. The cache will evict until it is within the new threshold.

You can get an entry snapshot using the `GetEntry` method. If you do not want to update statistics or eviction policy when receiving data, you can use the `GetEntryQuietly` method.

```go
entry, ok := cache.GetEntryQuietly(key)
if !ok {
	return
}
cache.SetRefreshableAfter(key, entry.ExpiresAfter() / 2)
```

Also, if `ExpiryCalculator` or `RefreshCalculator` are not suitable for you, then you can use the `SetExpiresAfter` or `SetRefreshableAfter` methods, respectively.
