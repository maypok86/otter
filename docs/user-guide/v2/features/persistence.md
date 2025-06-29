---
tags:
  - v2
---

# Persistence

```go
const filePath = "cache.gob"

cache := otter.Must(&otter.Options[string, string]{
    MaximumSize:      10_000,
    ExpiryCalculator: otter.ExpiryWriting[string, string](time.Hour),
    StatsRecorder:    stats.NewCounter(),
})
if err := otter.LoadCacheFromFile(cache, filePath); err != nil {
    panic(err)
}

// ...

if err := otter.SaveCacheToFile(cache, filePath); err != nil {
    panic(err)
}
```

You can save the cache to a file and load the cache from a file. This is useful for avoiding cold starts after application restarts. You can also implement your own custom file persistence logic based on these functions' source code.
