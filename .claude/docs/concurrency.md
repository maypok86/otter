# Concurrency model

Mechanical facts about how otter synchronizes. Audits read this during analysis; it says what
the code does, not whether it is right.

## Locks

Order: `evictionMutex` → hash table bucket lock → singleflight bucket lock. Nothing takes them
in another order.

| Lock | Guards | Taken by |
|---|---|---|
| `cache.evictionMutex` | the eviction policy (queues, sizes, sketch, climber), the timer wheel, `policyWeight`, `queueType`, node `dead` transition | `maintenance` (`performCleanUp`, `scheduleDrainBuffers` via `TryLock`), `SetMaximum`, `Hottest`/`Coldest`, `WeightedSize`/`GetMaximum` |
| `hashmap` bucket lock (`internal/hashmap/map.go`, inside `Compute`) | the bucket's slots; every write to a key; node value/weight/deadline changes made in place; the `retired` transition | `set`, `doCompute`, `Invalidate`, `afterDeleteCall`, `deleteNodeFromMap` |
| singleflight bucket lock | in-flight `call`s per key | `startCall`, `deleteCall`, `delete`, `deleteRefresh` |

`hashmap.Get` takes no lock: it reads bucket slots atomically. Readers therefore see a node
that a writer may be changing or replacing at that moment.

## Node lifecycle

`alive` → `retired` → `dead`, never backwards.

- `retired`: removed from the hash table (`makeRetired`, under the bucket lock).
- `dead`: removed from the policy and the wheel (`makeDead`, under `evictionMutex`).

A node's **weight is accounted in the policy if and only if the node is linked into a queue**
(`policy.adjustAccounted`). Weighted nodes keep two weights: `weight` (writer's view, atomic,
set under the bucket lock) and `policyWeight` (what the policy accounted, under
`evictionMutex`). `policy.reweigh` reconciles them; it is idempotent and order-independent.

## Node layout (`cmd/generator`)

One node type per feature set (`B`, `BS`, `BSE`, ...). It is the header and the only type that
implements `Node`. Nodes with state (size, weight or expiration) are allocated as one of the
layouts whose first field is that header; an immutable `variant` byte records which:

| Variant | Value | `CanSetValue` |
|---|---|---|
| `Inline` | `value V`, never written after publication | false |
| `Boxed` | `atomic.Pointer[V]` | true |
| `P` | pointer-shaped value in an atomic `unsafe.Pointer` | true |
| `U64` | pointer-free value of ≤ 8 bytes in `atomic.Uint64` | true |
| `Empty` | nothing (zero-size `V`) | true |

`Value`/`SetValue` convert the header pointer back to the layout it was taken from.
`SetValue` on a published node is legal only when `CanSetValue` is true. Nodes without state
(`B`, `BR`) keep the value inline in the node type itself and are never updated in place.

## Read path

`GetIfPresent` → `getNode`: lock-free `hashmap.Get`, `HasExpired`, then `afterRead`, which
recalculates the deadline (`ExpireAfterRead`, CAS on `expiresAt`) and offers the node to the
lossy read buffer. A full buffer drops the access; it never blocks.

## Write path

1. `hashmap.Compute` locks the bucket and calls `set` / `doCompute` / `afterDeleteCall`.
2. Under the lock, `atomicSet` decides between an **in-place update** (old node alive and
   `CanSetValue`: value, weight, deadline and refresh time change on the same node, and
   `OnAtomicDeletion` runs first) and a **replacement** (a new node, the old one retired). An
   expired node that is still in the table is reused in place ("resurrection"). The decision,
   the deletion cause and whether reconciliation is needed are returned as `writeResult`.
3. After the lock is released, `afterWrite`:
   - in place: an access through the read buffer, plus a `reconcileReason` task if the weight
     changed or the deadline moved earlier;
   - insert: an `addReason` task; replacement: an `updateReason` task.
4. Tasks go to the bounded MPSC write buffer. When it is full, the writer runs
   `performCleanUp` itself under `evictionMutex`.

User callbacks under the bucket lock: the weigher, `ExpiryCalculator.ExpireAfterCreate/Update`
(and `ExpireAfterRead` when `SetIfAbsent` finds a live entry),
the `RefreshCalculator`, `OnAtomicDeletion`, and the `Compute` remapping function (whose panic
is recovered). `OnDeletion` and loaders run on the executor (default `go fn()`); with a
synchronous executor they run inline, possibly under `evictionMutex`.

## Maintenance

`maintenance` (under `evictionMutex`): drain the read buffer (`onAccess`), drain the write
buffer (`runTask`), run the caller's task, expire (`expireNodes`), evict (`evictNodes`), climb.
`drainStatus` (`idle`, `required`, `processingToIdle`, `processingToRequired`) decides whether
another drain must be scheduled.

The timer wheel reads a node's deadline before taking the bucket lock, so `expireNode`
re-checks expiration under the lock (`deleteNodeFromMap(..., onlyIfExpired)`) and reschedules a
node that a writer extended in the meantime.

## Loading and refresh

`Get`/`BulkGet` with a loader start a singleflight `call`; waiters block on it. Completion goes
through `afterDeleteCall`, which writes the result with `atomicSet` only if the call is still
the current one for the key (`deleteCall` identity check). `Invalidate`, `Set` and `Compute`
cancel a pending call for the key; eviction and expiration drop only refresh calls
(`deleteRefresh`).

A refresh must lose to any write that follows its request, so three things hold together:
- refresh calls are registered on the requesting goroutine, before the task goes to the
  executor (`refreshKey`, `bulkRefreshKeys`; `executeRefresh` finishes them if the executor
  panics);
- `atomicSet` cancels pending calls only after publishing the new value (`cancelCalls`), so a
  refresh requested during an in-place write, e.g. from `OnAtomicDeletion`, is cancelled too;
- a refresh call records the node it was requested for (`call.base`), and `afterDeleteCall`
  writes its result only if that node is still the key's node (`isRefreshBase`), which covers
  a replacement or removal published after the write's callback returns.
