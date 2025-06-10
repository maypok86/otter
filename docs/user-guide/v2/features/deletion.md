---
tags:
  - v2
---

# Deletion

Terminology:

- **eviction** means deletion due to the policy
- **invalidation** means manual deletion by the caller
- **deletion** occurs as a consequence of invalidation or eviction

## Invalidation

At any time, you may explicitly invalidate cache entries rather than waiting for entries to be evicted.

```go
// individual key
cache.Invalidate(key)
// all keys
cache.InvalidateAll()
```

## Deletion Handlers

```go
cache := otter.Must(&otter.Options[string, string]{
	OnDeletion: func(e otter.DeletionEvent[string, string]) {
		fmt.Printf("Key %s was deleted (%s)\n", e.Key, e.Cause)
    },
    OnAtomicDeletion: func(e otter.DeletionEvent[string, string]) {
        fmt.Printf("Key %s was deleted (%s)\n", e.Key, e.Cause)
    },
})
```

You may specify a deletion handler for your cache to perform some operation when an entry is deleted, via `OnDeletion`. These operations are executed asynchronously using a goroutine.

When the operation must be performed synchronously with deletion, use `OnAtomicDeletion` instead.
