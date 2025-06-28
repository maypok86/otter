---
tags:
  - v2
---

# Compute

```go
cache := otter.Must(&otter.Options[string, string]{
    OnAtomicDeletion: func(e otter.DeletionEvent[string, string]) {
        if !e.WasEvicted() {
            return
        }
        // atomically intercept the entry's eviction
    }
})
```

Otter offers extension points for complex workflows that require an external resource to observe the sequential order of changes for a given key. For manual operations, hash table `Compute*` methods provides the ability to perform logic within an atomic operation that may create, update, or remove an entry. When an entry is automatically removed, an `OnAtomicDeletion` handler expands the map computation to execute custom logic. These types of entry operations will mean that the cache will block subsequent mutative operations for the entry and reads will return the previous value until to write completes.

## Possible Use-Cases

### Write Modes

A computation may be used to implement a write-through or write-back cache.

In a write-through cache the operations are performed synchronously and the cache will only be updated if the writer completes successfully. This avoids race conditions where the resource and cache are updated as independent atomic operations.

In a write-back cache the operation to the external resource is performed asynchronously after the cache has been updated. This improves write throughput at the risk of data inconsistency, such as leaving invalid state in the cache if the write fails. This approach may be useful to delay writes until a specific time, limit the write rate, etc.

A write-back extension might implement some or all of the following features:

- Delaying the operations until a time window
- Performing a batch prior to a periodic flush if it exceeds a threshold size
- Loading from the write-behind buffer if the operations have not yet been flushed
- Handling retrials, rate limiting, and concurrency depending on the characteristics of the external resource.

### Layering

A layered cache loads from and writes to an external cache that is backed by the system of record. This allows having a small fast cache that falls back to slow large cache. Typical tiers are off-heap, file-based, and remote caches.

A victim cache is a layering variant where the evicted entries are written to the secondary cache. The `OnAtomicDeletion` handler's `DeletionCause` allows for inspecting why the entry was deleted and reacting accordingly.

### Synchronous Handlers

A synchronous handler receives event notifications in the order that the operations occur on the cache for a given key. The handler may either block the cache operation or queue the event to be performed asynchronously. This type of handler is most often used for replication or constructing a distributed cache.
