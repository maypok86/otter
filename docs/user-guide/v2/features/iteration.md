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
3. **Weak Consistency**: Iteration reports every key that is present for the whole iteration once; changes made during the iteration may or may not be reported
4. **Memory Efficiency**: Iteration is performed without copying the entire cache
