---
name: audit-liveness
description: Prove or break progress properties (maintenance terminates, entries get evicted and expired, loads and refreshes finish, notifications are delivered, nobody starves)
context: fork
agent: auditor
disable-model-invocation: true
---

# Audit: liveness

Other audits ask whether the cache can reach a bad state. This one asks only whether good
things eventually happen. Do not report safety defects here, and do not suggest
improvements: either prove a property holds or construct an execution that violates it.

Adapted from Caffeine's `audit-liveness`.

## Assumptions to state

Progress in otter depends on its environment. For every property, name what it relies on and
check that the dependency is documented:

- the executor runs every task it is given, eventually, and does not block the submitter
  (`Options.Executor`); say what happens when it runs tasks inline, late, or never;
- user code returns: loaders, calculators, the weigher, both listeners, the clock, the logger
  and the stats recorder;
- the clock's `Tick` delivers ticks (`clock.go`), which drive `periodicCleanUp`;
- the goroutine scheduler is fair enough for `runtime.Gosched` in `afterWriteTask` to let the
  maintainer run.

A violation that needs an assumption the docs already forbid is a documentation finding at
most. One that needs only legal behavior (a slow but correct executor, a clock that never
ticks between operations, a loader that takes a minute) is a defect.

## Properties

1. **Maintenance terminates.** `maintenance` drains at most `maxWriteBufferSize` tasks per
   pass (`drainWriteBuffer`); `afterWriteTask` retries `writeBufferRetries` times before the
   writer drains itself (`performCleanUp`). Can a pass, or a writer stuck in that loop, run
   without bound while other writers keep the buffer full? Can `expireNodes` or `evictNodes`
   loop without bound (a node the wheel reschedules into the bucket it is draining, a victim
   that is never removed)?
2. **Eviction progress.** If `weightedSize` exceeds the maximum and no new entries arrive, does
   it eventually fall to the maximum? A zero-weight node is never evicted for size, and
   `removeForSize` declines a node a writer has just set to zero weight (`concurrency.md`,
   "Maintenance"): can such declines, or an entry heavier than the maximum, keep the cache over
   its bound forever? With a custom executor, `rescheduleCleanUpIfIncomplete` does not
   reschedule: if writes stop right after a pass that left `required`, who drains the rest?
3. **Expiration progress.** An expired entry is absent to every caller, but is it eventually
   removed and reported? Periodic clean-up runs only with expiration, once per tick; with a
   `Tick` that panics or never fires, or after `StopAllGoroutines`, removal waits for the next
   operation. Is that documented? Can the wheel keep an expired node forever (a node scheduled
   past the wheel's span, a deadline that `expireTolerance` keeps moving)?
4. **Load and refresh progress.** Does every registered call finish, so that waiters wake up?
   Trace a refresh whose executor task panics (`group.submitRefresh`), never runs, or runs
   after the key was invalidated; a bulk load whose loader returns fewer or more keys; a load
   whose owner's context ends. Can a `Get` that joined a call loop forever between joining and
   loading again (`failedForOwner`, `expiredBefore`, `refused`)?
5. **Buffers.** Can the write buffer stay full with nobody draining it, so every writer pays
   for `performCleanUp`, for as long as one maintenance pass holds `evictionMutex`? The read
   buffer is lossy by design: can it lose all accesses of a hot key for long enough to change
   which entries are evicted, for example while the sketch is not initialized
   (`skipReadBuffer`)?
6. **Starvation.** Can `evictionMutex` be held for an unbounded time? `Hottest` and `Coldest`
   call the caller's loop body under it, and a synchronous executor runs `OnDeletion` under it
   (`options.go`). Bound the wait of a writer that needs the mutex in each case, and of a
   reader that needs a bucket lock held by a long `Compute` function or a slow calculator.
7. **Notification progress.** Can an `OnDeletion` event be lost, rather than delayed? Check
   every path that removes an entry or replaces a value, including a panic in maintenance, an
   executor that panics while running the notification, and a cache that becomes unreachable
   while events are queued.

## For each property

- The mechanism that ensures progress: a bound, a retry limit, a CAS that must succeed after
  finitely many failures, the periodic tick, a fallback to the caller.
- The conditions it needs, and whether the docs state them.
- A proof, or a violating execution: the goroutines, the steps and the lines, and what never
  happens. A violating execution needs a witness under
  `.local/audits/<model>/audit-liveness-evidence/`, a test that times out or shows the stuck
  state. A frozen fake clock or a caller-runs executor can stop what would otherwise make
  progress: use them to steer the schedule, then confirm with the default executor and the real
  clock (`finding-taxonomy.md`).
