---
tags:
  - v2
---

# Eviction

Otter provides two types of eviction: size-based eviction and time-based eviction.

## Size-based

```go
// Evict based on the number of entries in the cache.
cache := otter.Must(&otter.Options[string, string]{
	MaximumSize: 1000,
})

// Evict based on the total length of key-value pairs in the cache.
cache := otter.Must(&otter.Options[string, string]{
	MaximumWeight: 10 * 1024 * 1024, // 10MB
	Weigher: func(key string, value string) uint32 {
		return uint32(len(key)+len(value))
    },
})
```

If your cache should not grow beyond a certain size, use `Options.MaximumSize`. The cache will try to evict entries that have not been used recently or very often.

Alternately, if different cache entries have different "weights" -- for example, if your cache values have radically different memory footprints -- you may specify a weight function with `Options.Weigher` and a maximum cache weight with `Options.MaximumWeight`. In addition to the same caveats as `MaximumSize` requires, be aware that weights are computed at entry creation and update time, are static thereafter, and that relative weights are not used when making eviction selections.

## Time-based

```go
// Evict based on a varying expiration policy.
cache := otter.Must(&otter.Options[string, string]{
	ExpiryCalculator: otter.ExpiryWriting[string, string](time.Hour),
})
```

Entries are expired after the variable duration has passed. Expiration is performed with periodic maintenance, during writes and occasionally during reads. The expiration operation is executed in amortized O(1) time.

## Pinning Entries

A pinned entry is one that cannot be deleted by an eviction policy. This is useful when the entry is a stateful resource, like a lock, that can only be discarded after the client has finished using it. In those cases the behavior of evicting an entry and recomputing it would cause a resource leak.

An entry can be excluded from maximum size eviction by using weights and evaluating the entry to a weight of zero. The entry then does not count towards the overall capacity and is skipped by the maximum size eviction. A custom `Weigher` must be defined that can evaluate if the entry is pinned.

An entry can be excluded from expiration by using a duration of `math.MaxInt64`, or roughly 300 years. A custom `ExpiryCalculator` must be defined that can evaluate if the entry is pinned.

The weight and expiration are evaluated when the entry is written into the cache.
