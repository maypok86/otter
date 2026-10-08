---
name: audit-reentrancy
description: Analyze user callbacks for re-entrancy defects (deadlock, corruption, torn observation)
context: fork
agent: auditor
disable-model-invocation: true
---

Analyze the cache for defects caused by user callbacks calling back into the cache.

## Callbacks

1. `Loader.Load` / `Reload`, `BulkLoader.BulkLoad` / `BulkReload`
2. `Weigher`
3. `ExpiryCalculator.ExpireAfterCreate / ExpireAfterUpdate / ExpireAfterRead`
4. `RefreshCalculator` methods
5. `OnDeletion` (default executor and a synchronous executor)
6. `OnAtomicDeletion`
7. The functions passed to `Compute`, `ComputeIfAbsent`, `ComputeIfPresent`
8. A custom `Executor` (a caller-runs executor), `Clock`, `StatsRecorder`, `Logger`

## For each callback

1. List every lock held when it is invoked: `evictionMutex`, the hash table bucket lock (which
   bucket), the singleflight bucket lock. Use `.claude/docs/concurrency.md` and confirm in code.
2. Determine what happens if the callback calls each of: `GetIfPresent`, `Get` (with a loader),
   `Set`, `SetIfAbsent`, `Compute` on the same key and on another key in the same bucket,
   `Invalidate`, `InvalidateAll`, `SetExpiresAfter`, `CleanUp`, `EstimatedSize`,
   `WeightedSize`, `GetMaximum`, `SetMaximum`, `All`/`Keys`, `Hottest`/`Coldest`, `Refresh`.
3. For each pair where locks are held:
   - Can it deadlock (the same non-reentrant lock again, or the lock order violated)?
   - Can it corrupt state (re-entering mid-mutation, e.g. inside `atomicSet` between
     `OnAtomicDeletion` and `SetValue`)?
   - Can it observe a half-applied write?
4. If the cache defends against re-entrancy (deferring work to the executor, documenting a
   restriction), check that the defense covers every path, including the synchronous
   executor and maintenance run by a writer in `performCleanUp`.

Separate defects from documented restrictions: a deadlock the docs forbid ("must not call the
cache") is a documentation finding at most. A deadlock the docs do not mention is a defect.

For each defect: the callback, the re-entrant method, the locks involved, the call stack, and
the observable result.
