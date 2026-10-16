# Concurrency model

Mechanical facts about how otter synchronizes. Audits read this during analysis; it says what
the code does, not whether it is right.

## Locks

Order: `evictionMutex` → hash table bucket lock → singleflight bucket lock. Nothing takes them
in another order.

| Lock | Guards | Taken by |
|---|---|---|
| `cache.evictionMutex` | the eviction policy (queues, sizes, sketch, climber), the timer wheel, `policyWeight`, `queueType`, node `dead` transition | `maintenance` (`performCleanUp`, `scheduleDrainBuffers` via `TryLock`), `SetMaximum`, `Hottest`/`Coldest`, `WeightedSize`/`GetMaximum` |
| `hashmap` bucket lock (`internal/hashmap/map.go`, inside `Compute`) | the bucket's slots; every write to a key; node value/weight/deadline/refresh time changes, except a read's `ExpireAfterRead` CAS; the `retired` transition | `set`, `doCompute`, `Invalidate`, `commitCall`, `deleteNodeFromMap`, `SetExpiresAfter`, `SetRefreshableAfter` |
| singleflight bucket lock | in-flight `call`s per key | `startLoad`, `startRefresh`, `deleteCall`, `delete`, `deleteOnRemoval` |

`hashmap.Get` takes no lock: it reads bucket slots atomically. Readers therefore see a node
that a writer may be changing or replacing at that moment.

## Node lifecycle

`alive` → `retired` → `dead`, never backwards.

- `retired`: removed from the hash table (`makeRetired`, under the bucket lock).
- `dead`: removed from the policy and the wheel (`makeDead`, under `evictionMutex`).

A node is retired inside the hash table callback, before the table stores its replacement or
clears its slot, so a retired node can still be the key's node for a moment. Readers that look a
key up treat it as present (`getNode`, `getNodeQuietly`); iterations, which find a node before
they report it, report the key's current node instead (`cache.current`).

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
recalculates the deadline (`ExpireAfterRead`, CAS on `expiresAt`, refused if the entry expired
while the calculator ran; skipped for `ExpiryCreating` and `ExpiryWriting`, whose reads keep it,
see `withReadExpiry`) and offers the node to the
lossy read buffer. A full buffer drops the access; it never blocks.

## Write path

Every change decided from a key's current value (`set`, `doCompute`, `Invalidate`,
`commitCall`) is a key mutation (`mutation.go`):

1. `mutate` locks the bucket (`hashmap.Compute`), reads the clock and the current node once
   (absent, live or expired) and asks the caller's `decide` for an op: keep, write, invalidate or remove an
   expired entry. Its `origin` (caller, load or refresh) decides which pending calls are
   cancelled and whether a write is a reload.
2. Under the lock, `atomicSet` decides between an **in-place update** (old node alive and
   `CanSetValue`: value, weight, deadline and refresh time change on the same node, and
   `OnAtomicDeletion` runs first) and a **replacement** (a new node, the old one retired). An
   expired node that is still in the table is reused in place ("resurrection"). The decision,
   the deletion cause and whether reconciliation is needed are returned in the `mutation`.
3. After the lock is released, `publish` (`afterWrite` for a write, `afterDelete` for a
   removal):
   - in place: an access through the read buffer, plus a `reconcileReason` task if the weight
     changed or the deadline moved earlier; a write over an expired entry in a cache with
     eviction (a reload) skips the read buffer and always sends the `reconcileReason` task,
     tagged `accessReload`, so that the climber counts a miss rather than a hit;
   - insert: an `addReason` task; replacement: an `updateReason` task;
   - the completion of a refresh is not an access, since the read that started it was one: in
     place it skips the read buffer (the timer wheel reschedules a deadline that moved later
     when it finds the node unexpired), and its `updateReason` task carries `accessQuiet`, so
     `policy.update` only reorders the node (a replaced reload carries `accessReload`);
   - `OnDeletion` of the previous value is submitted here on every path, not when maintenance
     replays the task, and before the task is handed to the write buffer, where maintenance
     running on the writer (a caller-runs executor, a full buffer) could evict the new value
     first. The events that writers submit for one key thus reach the executor in the order of
     the mutations, for mutations ordered by happens-before; two goroutines that write a key
     submit after the lock is released, in either order. Maintenance submits only the
     removals it decides itself (evictions, expirations found by the timer wheel). On another
     goroutine it can make one between a write's release of the lock and its submission: a
     node updated in place stays linked, so its eviction or expiration can be reported before
     the value the update replaced. The task is handed off in a `defer`, so a listener or an
     executor that calls `runtime.Goexit` does not lose it.
4. Tasks go to the bounded MPSC write buffer. When it is full, the writer runs
   `performCleanUp` itself under `evictionMutex`.

User callbacks under the bucket lock: the weigher, `ExpiryCalculator.ExpireAfterCreate/Update`
(and `ExpireAfterRead` when `SetIfAbsent` finds a live entry, unless reads keep the deadline),
the `RefreshCalculator`, `OnAtomicDeletion`, and the `Compute` remapping function (whose panic
is recovered). `OnDeletion` and loaders run on the executor (default `go fn()`); with a
synchronous executor they run inline, possibly under `evictionMutex`.

`SetExpiresAfter` and `SetRefreshableAfter` take the bucket lock: a write reads the deadline and
refresh time before its calculators run and stores the results after them, so a change made
without the lock in that window would be lost. Like a write, they deadlock when called from a
callback that holds the lock of the same bucket.

## Maintenance

`maintenance` (under `evictionMutex`): drain the read buffer (`onAccess`), drain the write
buffer (`runTask`), run the caller's task, expire (`expireNodes`), evict (`evictNodes`), climb.
`drainStatus` (`idle`, `required`, `processingToIdle`, `processingToRequired`) decides whether
another drain must be scheduled.

`scheduleDrainBuffers` takes `evictionMutex` with `TryLock` and hands `drainBuffers` to the
executor, which may run it inline or later. Who releases the mutex is decided by `drainToken`:
the scheduler stores a new even value while it holds the mutex, and whoever makes it odd with a
CAS releases the mutex. A drain that runs inline finds the mutex held and the token even, takes
the release over and runs maintenance under the scheduler's lock; the scheduler's own CAS then
fails, and it does not release the mutex a second time. Otherwise the scheduler releases it once
the executor returns, and the drain takes the mutex itself (`TryLock`, or `performCleanUp` if it
is held by then). The token is even only while a scheduler holds the mutex and has not released
it, since the other holders (`performCleanUp`, `CleanUp`, `SetMaximum`) never change it. Each
schedule takes a new value, so a scheduler that resumes late cannot release the mutex of a later
schedule. Only a drain can take over another schedule's release, for example a late async drain
that runs while a newer scheduler holds the mutex, and the mutex is still released once. If the
executor panics, the scheduler releases the mutex unless a drain has taken it over, and leaves
the drain `required`. `drainBuffersFunc` is the method value bound once: taking `c.drainBuffers`
on every schedule would allocate.

The timer wheel (`internal/expiration`) decides where a node is scheduled: `Schedule` puts a live
node in the bucket of its current deadline, moving it if it is already scheduled, and unschedules
a node that is not alive. Because it is idempotent, accesses and writes can reach it out of order:
an access replayed before the node's insertion schedules it early, and the insertion only moves
it. The wheel can then expire the node before the insertion is replayed; the policy's `add` skips
a node that is not alive.

The wheel reads a node's deadline before taking the bucket lock, so `expireNode` re-checks
expiration under the lock (`deleteNodeFromMap(..., removeExpired)`) and returns false for a node
that a writer extended in the meantime; `Advance` then schedules it again.

The eviction policy evicts through the `evict` callback it is built with (`cache.evictNode`). The
cache removes the node from the hash table and the wheel and reports it, and the policy unlinks
the node when the callback returns true; the cache does not call back into the policy during its
walk. The callback returns false for a node whose weight a writer has set to zero in place since
the policy accounted it (`removeForSize`): a zero-weight entry is never evicted for size, and that
must be decided under the bucket lock, atomically with the writer. The node stays linked until its
reweigh task brings the accounted weight to zero.

## Loading and refresh

`group` (`singleflight.go`) owns the lifecycle of a `call`: `Get`/`BulkGet` register one with
`startLoad`, refreshes with `startRefresh`; waiters block on it until it finishes or their own
context is done (`waitContext`). A load that fails after its owner's context is done
(`failedForOwner`) is not shared: a waiter with a live context loads the key again. Refreshes
run with `context.WithoutCancel`, so their failures are always shared. Nothing that can panic runs
between registering a call and the defer in `doCall`/`doBulkCall` that finishes it, or the call
would stay registered and every later load of its key would wait for it: a refresh is detached
from cancellation inside them (`WithoutCancel` panics on a nil context), and loaders are passed in
closures, not as method values (the method value of a nil loader panics where it is taken).
`doCall`/`doBulkCall` run the loader with its panics and `runtime.Goexit` caught and end in
`group.finish`, which commits the result through the cache's `commitCall` (a key mutation from a load or refresh), then deletes
the call, wakes its waiters and publishes. `commitCall` writes only if `group.authoritative`
says the call is still the current one for the key. A load writes only over a key without a
live value: a live value was written during the load, and the load returns it to its owner and
waiters, whatever it loaded, so the `Get` is a hit at the commit. A `Get` that owns or joined a
call whose value the cache refuses (`call.refused`: a cancelled load, or a refresh that was
cancelled or whose node is gone) does not take that value either: it returns the key's live
value, or loads the key again. A load cancelled by an invalidation may have read the data
source before the change, and `Invalidate` has reported the key absent. Nor does a `Get` that joined a load after its value was
committed and had expired before the `Get` started (`call.expiresAt`): the call stays registered
until `finish` deletes it, after the commit. The call also records the value's node (`call.node`),
whose deadline counts as well: `SetExpiresAfter` or `ExpireAfterRead` can move it earlier while
the call is registered. A write cancels the calls
before a new node is stored (`hashmap.Compute` stores it after the callback), so a load
registered in between is not cancelled, and only this rule keeps it from overwriting the write.
Such a call can also find the old value on its re-check and adopt it, so `mutate` cancels the
key's calls a second time once the write or invalidation from the API is visible: a `Get` that
starts after it returned does not join a call that holds the old value.
`Invalidate`, `Set` and `Compute` cancel a
pending call for the key; eviction and expiration drop refreshes of an existing
node and calls that have already committed (`deleteOnRemoval`), but not a load in flight or a
refresh of an absent key, which loads it just the same. A committed load stays
registered until `finish` deletes it, and a `Get` that joined it after the removal would return
the removed value; `committed` is set by `commitCall` under the key's bucket lock, which the
removal holds too. A load that adopts a value it finds after winning its call slot reads it
under that lock too (`readForAdoption`) and is marked committed there, for the same reason. A refresh registered after the write or removal that would have cancelled it
cannot write once its base is removed or replaced, so the next call for the key replaces it
instead of joining it (`group.superseded`); a read that finds a node being removed does not
refresh it.
A fake call (a key the bulk loader returned without being asked) has no call for an invalidation
to cancel: it fills only a key without a live value or a call, and only if no key has been
invalidated since its bulk load started (`group.invalidations`).

A refresh must lose to any write that follows its request, so three things hold together:
- refresh calls are registered on the requesting goroutine, before the task goes to the
  executor (`startRefresh` in `refreshKey`, `bulkRefreshKeys`; `group.submitRefresh` finishes
  them if the executor panics);
- `atomicSet` cancels pending calls only after an in-place update has published the new value
  (`cancelCalls`), so a refresh requested during the write, e.g. from `OnAtomicDeletion`, is
  cancelled too; a replacing node is stored only after the callback, which the next point covers;
- a call records the node it was started for (`call.base`, nil for a load), and its result is
  written only if that node is still the key's node and has not expired (`isRefreshBase` in
  `group.authoritative`), which covers a replacement or removal published after the write's
  callback returns. A load therefore never writes over a live value.

`isRefreshBase` compares node identity, so it does not see the base updated in place. While a
refresh is registered, only a write from the API changes the base's value in place, and that
write cancels the refresh: `cancelCalls` runs after the new value is published. Loads and
refreshes of the key join the registered call, a fake call writes only when the key has no call
of its own, and `SetExpiresAfter`/`SetRefreshableAfter` change deadlines, not the value. The
exception is a write whose cancellation comes before the refresh is registered: the refresh is
not cancelled and its result replaces the written value. That is why a reload reads the base's
value when it starts (`base.Value()`, `rk.old.Value()`), not when it was requested: the loader
gets the value its result replaces.
