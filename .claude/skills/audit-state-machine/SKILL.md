---
name: audit-state-machine
description: Build the transition table of each explicit state machine (drain status, drain token, node lifecycle, load and refresh calls) and look for illegal or missed transitions
context: fork
agent: auditor
disable-model-invocation: true
---

# Audit: state machines

Field-by-field audits check one site at a time. This one builds the full transition table of
each machine and asks whether every reachable interleaving keeps it legal. A missed transition
wedges the cache: work stays buffered and nobody drains it. An illegal one resurrects a dead
node, releases a mutex twice, or leaves a waiter blocked on a call that never finishes.

Adapted from Caffeine's `audit-state-machine`.

For every machine below:

1. Build the table: for each (state, event) pair, the next state, the goroutine that drives it
   and the line that does it. Events include a write, a read, the start and end of maintenance,
   the periodic tick, an executor that runs the task inline, later or never, a panic or
   `runtime.Goexit` in user code, and a full write buffer.
2. Confirm every transition site in code. `.claude/docs/concurrency.md` describes the intent;
   the code is the authority.
3. Attack with the questions listed for the machine, then with your own.

## Machine 1: drain status (priority)

`cache.drainStatus` (`cache_impl.go`): `idle`, `required`, `processingToIdle`,
`processingToRequired`. Transition sites: `scheduleAfterWrite`, `scheduleDrainBuffers`,
`maintenance`, `drainWriteBuffer`, `rescheduleCleanUpIfIncomplete`, `shouldDrainBuffers`
(read side), and the panic paths of `scheduleDrainBuffers` and `maintenance`. One event
changes the buffers without a transition: `invalidateAllLocked` replays the write buffer
(`runTask`) under `evictionMutex`, outside `maintenance`, and leaves the status as it found it.
Plain atomic loads and stores mix with CAS: name, for every store, why no concurrent transition
it overwrites matters.

1. **Lost wakeup.** Can the status settle in `idle` while a task sits in the write buffer, or
   in `required` with nobody scheduled to drain it? Trace the exit CAS of `maintenance`
   (`processingToIdle → idle`) against a `scheduleAfterWrite` that read `processingToIdle`, and
   against a writer that pushed its task after `drainWriteBuffer` emptied the buffer but before
   the exit CAS.
2. **Blind stores.** `maintenance` stores `processingToIdle` on entry and `scheduleDrainBuffers`
   stores it after `TryLock`. Can either overwrite a `processingToRequired` or `required` set by
   a writer whose task the pass will not see?
3. **One consumer.** The write buffer is single-consumer. Can two goroutines pop it at once?
   Every consumer must hold `evictionMutex`: the callers of `maintenance` (`performCleanUp`,
   `drainBuffers`, `GetMaximum`, `WeightedSize`, `SetMaximum`, `Hottest`/`Coldest`, `warmUp` in
   `persistence.go`) and `invalidateAllLocked`. What status does `invalidateAllLocked` leave
   behind, and does a drain scheduled before it still do the right thing afterwards?
4. **Custom executor.** `rescheduleCleanUpIfIncomplete` reschedules only on the default
   executor. With a custom one, when does a `required` status left after a pass get drained,
   and by whom? Is that the documented behavior, or a cache that waits for the next operation?
5. **Panics.** A panic in `maintenance` leaves `required`. A panic or `Goexit` in the executor
   leaves `required` unless the task had started. Does every panic path leave a state from
   which the next operation drains?

## Machine 2: drain token

`cache.drainToken` decides who releases `evictionMutex` after `scheduleDrainBuffers` hands
`drainBuffersFunc` to the executor: the scheduler, or a drain that took the mutex over
(`drainBuffers`, the even/odd CAS). See `concurrency.md`, "Maintenance".

1. Can the mutex be released twice, or never: a drain that runs inline and one that runs later
   on another goroutine, an executor that runs the task twice, one that runs it after the
   scheduler's deferred CAS, one that panics after running it?
2. Can a stale token from an earlier schedule match a later one (ABA across two schedules)?
3. Does a drain that loses the CAS always fall back to a path that takes the mutex itself, and
   can it then deadlock against the scheduler that still holds it?

## Machine 3: node lifecycle

alive → retired → dead, one way only. Generated `IsAlive`/`IsRetired`/`IsDead`, `Retire`, `Die`
(`cmd/generator/node.go`); `makeRetired` and `makeDead` (`cache_impl.go`); `policy.makeDead`,
`policy.updateNode` (`policy.go`); the wheel's `IsAlive` check (`internal/expiration/wheel.go`).
In-place updates reuse a live node (`CanSetValue`, `atomicSet`) when its deadline does not move
earlier; a write over an expired node that is still in the table, or one that moves the deadline
earlier, replaces it.

1. Can any path move a node backwards (dead → alive, retired → alive), or give a retired or
   dead node a new value in place?
2. A node removed from the hash table must reach `dead`. Find every removal path (invalidate,
   replace, expire, evict, `InvalidateAll`, a load that finds the key not found) and the task
   that kills the node. Can a panic, an early return or a task dropped from the write buffer
   leave it retired forever, still linked in a policy queue or the wheel?
3. Is the node's weight added to and removed from `weightedSize` exactly once per lifetime,
   whatever order the add, update and delete tasks of one key are replayed in?
4. Without maintenance (`withMaintenance` false), nodes are never retired. Do the paths that
   read `IsAlive` in that configuration still mean what they assume?

## Machine 4: load and refresh calls

A `call` (`singleflight.go`): registered (`startCall`), loading, committed (`committed`, under
the key's bucket lock), finished (`done` closed by `cancel`). Whether it is still in the
registry is a second dimension: `finish` and `adopt` remove it before they close `done`, and a
write can remove it while it is still loading. A call leaves the registry in two ways:

- by identity: `deleteCall` removes it only if it is still the registered call (from `finish`,
  `adopt`, `submitRefresh`);
- by key: `group.delete` and `group.invalidate` remove whatever call is registered, without
  comparing, on every write and invalidation that cancels a load (`mutation.go`, `cache_impl.go`);
  `deleteOnRemoval` removes it on an eviction or expiration of the key; `startCall` replaces a
  refresh that is `superseded`, and a refresh that registers replaces any call started from
  another base (`call.base`): a load still in flight when the key has a live value, or a
  refresh of a node that has expired since. The replaced call is not cancelled: it completes
  for the callers that joined it, and its commit is refused or keeps the live value.

Flags that qualify the result: `refused`, `isNotFound`, `ownerCtxDone`, `isFake`,
`node`/`expiresAt`. `group.submitRefresh` adds a small machine of its own: `submitted`,
`started`, `abandoned`.

1. Is every registered call finished exactly once, on every path: success, error, not found,
   panic, `Goexit`, an executor that panics, never runs the task or runs it after
   `submitRefresh` abandoned it? A call never finished blocks its waiters forever; one finished
   twice panics on `close`.
2. Are the result fields written before `done` is closed on every path, and never after?
3. Can a removal by key drop a registration that the write or removal it follows did not
   supersede: a load registered after the write cancelled the earlier one, or a call of a bulk
   load that is still running for other keys? Waiters of a removed call keep it: can they still be
   woken, and with which result?
4. `committed` decides whether an eviction or expiration drops the call (`deleteOnRemoval`).
   Can a call be committed after its value was removed, or uncommitted while its value is in
   the cache, so that a `Get` joins it and takes a removed value?
5. Fake calls (`isFake`, bulk loads) do not close `done`. Can a waiter ever wait on one?

## For each finding

- The interleaving, goroutine by goroutine, with the line of every step.
- The illegal or missed transition, and the state the machine is left in.
- The observable consequence: a wedged cache, a lost notification, a waiter blocked forever, a
  resurrected node, a wrong `weightedSize`.
- Why the interleaving is legal under the Go memory model.
- A witness under `.local/audits/<model>/audit-state-machine-evidence/`, run with `-race`. Steer
  the schedule with a fake clock or a queued executor if needed, then confirm the consequence
  with the default executor and the real clock (`finding-taxonomy.md`). If the window is too
  narrow for a test, say so and give the schedule a test would need.
