---
name: audit-exception-safety
description: Audit panic safety and failure atomicity at every point where user code or the runtime can panic
context: fork
agent: auditor
disable-model-invocation: true
---

Audit the cache for panic-safety defects: for every point where a panic can start, determine
whether the cache is left consistent and every waiter can still make progress.

Assume at least one such bug exists. If you find none, explain for each callback below why no
panic leaves inconsistent state.

## Panic sources

User code:

1. `Weigher` (called under the hash table bucket lock in `atomicSet`)
2. `ExpiryCalculator.ExpireAfterCreate / ExpireAfterUpdate` (under the bucket lock) and
   `ExpireAfterRead` (on the read path without a lock, but under the bucket lock when
   `SetIfAbsent` finds a live entry)
3. `RefreshCalculator` methods (under the bucket lock: `atomicSet`, `afterDeleteCall`)
4. `OnAtomicDeletion` (under the bucket lock, and under `evictionMutex` during maintenance)
5. `OnDeletion` (on the executor; inline under `evictionMutex` with a synchronous executor)
6. The `Compute` / `ComputeIfAbsent` / `ComputeIfPresent` functions (under the bucket lock;
   `doCompute` recovers and re-panics)
7. `Loader.Load` / `Reload`, `BulkLoader.BulkLoad` / `BulkReload` (inside `wrapLoad`, on the
   caller's goroutine or the executor)
8. A custom `Executor` and a custom `Clock`
9. `StatsRecorder` and `Logger` implementations

Runtime: out-of-memory on node or box allocation, a nil map or interface in user code.

## For each panic site

1. List every lock held and every mutation already committed at that point (hash table slot,
   node fields changed in place, `retired` state, singleflight call registered, task pushed,
   `drainStatus`).
2. Find what unwinds them: a `defer` that unlocks, a `recover`, or nothing. In particular,
   check whether `internal/hashmap` `Compute` releases the bucket lock when the callback
   panics, and whether `evictionMutex` is released when maintenance panics.
3. Check for:
   - **Stuck locks**: a bucket or `evictionMutex` left locked, so later writes or maintenance
     hang.
   - **Stuck waiters**: a singleflight `call` whose waiters never wake (`cl.cancel()` or the
     wait group never reached).
   - **Half-applied in-place updates**: value written but weight or deadline not, or the
     reverse; `OnAtomicDeletion` already fired for a write that never happened.
   - **Phantom or orphan nodes**: a node in the table but not in the policy, or the reverse.
   - **Counter drift**: `weightedSize`, window or protected sizes out of sync.
   - **Lost or duplicated notifications.**
   - **Process crash**: a panic re-raised on an executor goroutine where nobody can recover it.
4. For recover-and-re-panic paths (`doCompute`, `wrapLoad`), verify the committed state is
   consistent, the original panic value is preserved, and it reaches a goroutine that the
   caller controls.

For each defect: the panic site, the locks and mutations committed, the inconsistent state, a
concrete scenario (configuration, calls, which callback panics), and the observable effect
after the caller recovers.
