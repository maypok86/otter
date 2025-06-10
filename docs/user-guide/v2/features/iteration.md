---
tags:
  - v2
---

# Iteration

```go
for key, value := range cache.All() {
	cache.Invalidate(key)
}
```

You can iterate over all entries in the cache using the `All` method.

## Key Features

Iteration in Otter provides:

1. **Efficiency**: Iteration is performed efficiently without blocking other operations
2. **Goroutine Safety**: Iteration is safe to use in concurrent environments
3. **Snapshot View**: Iteration provides a consistent view of the cache at a point in time
4. **Memory Efficiency**: Iteration is performed without copying the entire cache
