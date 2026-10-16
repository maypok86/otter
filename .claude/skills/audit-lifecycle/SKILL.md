---
name: audit-lifecycle
description: Audit the life of the cache itself (construction, StopAllGoroutines, collection by the garbage collector) and the operations and goroutines that outlive each step
context: fork
agent: auditor
disable-model-invocation: true
---

# Audit: cache lifecycle

Other audits follow entries. This one follows the cache: its construction, the goroutines it
starts, `StopAllGoroutines`, and its collection by the garbage collector, and what happens to
operations, tasks and goroutines that are still running at each step.

Adapted from Caffeine's `audit-lifecycle`.

Assume at least one lifecycle defect exists. If you find none, re-trace the periodic clean-up
goroutine and the cleanup registered in `New` against every place that can block, and
explain why neither can block the other.

## The lifecycle

- `New` (`cache.go`) builds the internal `cache` (`newCache`, `cache_impl.go`), wraps it in
  `Cache` and registers `runtime.AddCleanup` on the wrapper, which calls `StopAllGoroutines`.
- With expiration, `newCache` starts `periodicCleanUp`, which runs `CleanUp` on every tick of
  `Clock.Tick` until it receives from the unbuffered `doneStop`.
- `StopAllGoroutines` sends on `doneStop` once (`stopOnce`) and is documented as forbidden
  inside functions the cache runs.
- Other goroutines: the default executor (`go fn()`), the goroutines that wait for a joined
  refresh (`refreshKey`, `sendAfterJoined`), and whatever a custom executor starts.

## Questions

1. **Reachability.** `New` passes the internal `cache` as the `arg` of `runtime.AddCleanup`, and
   the runtime keeps `arg` alive: if the `Cache` is reachable from it, the `Cache` is never
   collected and the cleanup never runs. The internal `cache` holds the user functions from the
   options (listeners, calculators, the weigher, the executor, the logger, the stats recorder,
   the clock), so one that captures the `Cache` leaks the whole cache, with or without
   expiration; with expiration, the periodic goroutine leaks too. Is that documented? Does
   `arg` need to be the whole internal `cache`? Are there internal references from `cache`
   back to `Cache`?
2. **The cleanup blocks.** The registered cleanup blocks on `doneStop` until `periodicCleanUp`
   receives, and that goroutine may be inside `CleanUp`: waiting for `evictionMutex` (held by a
   `Hottest` loop or a long maintenance pass), running `OnDeletion` on a synchronous executor,
   or in a slow clock. `runtime.AddCleanup` asks cleanups not to block: depending on the Go
   version (`go.mod` allows 1.24), the wait delays other cleanups of the program, or all of
   them. Bound the wait. `Clock.Tick` is called once, before the loop selects on `doneStop`:
   can a `Tick` that does not return keep the goroutine from ever receiving, and the cleanup
   and `StopAllGoroutines` blocked forever?
3. **Stop from inside the cache.** `StopAllGoroutines` called from `OnDeletion` on a synchronous
   executor during a periodic `CleanUp` deadlocks: the sender is the receiver. It is documented
   as forbidden. Find the paths where user code runs on the periodic goroutine without the user
   choosing a synchronous executor, if any, and other functions that run there that the
   warning does not name.
4. **Operations after stop.** After `StopAllGoroutines`, does every operation still work, with
   expired entries removed only by later operations? Does a second call return false, and do
   concurrent calls from the user and from the cleanup stop the goroutine exactly once? Is
   anything else (refresh waiters, executor tasks) left running that the name promises to stop?
5. **Operations during collection.** Can the cleanup run while an operation is still in progress
   on the `Cache`, for example after the caller's last use of the wrapper inside a long
   `Get`, `Compute` or iteration? Once the goroutine is stopped, does anything still expect
   periodic clean-ups? Cleanups run outside `testing/synctest` bubbles, and a cache with
   expiration created inside a bubble owns a bubbled `doneStop`: what happens when such a cache
   is collected before `StopAllGoroutines`, which its godoc suggests for synctest?
6. **Construction.** `newCache` starts the goroutine before `SetMaximum` runs and before
   `New` registers the cleanup. Can anything between them panic and leak the goroutine?
   `Must` panics on invalid options: is validation done before anything is started?
7. **In-flight work.** A load, a refresh or an executor task still running when the cache is
   collected keeps the internal `cache` alive until it ends. Does it end, and does its result
   go anywhere (a refresh commits into a cache nobody reads; an `OnDeletion` event arrives
   after the user dropped the cache)? Is any of it observable as a leak?
8. **Persistence.** `SaveCacheToFile` and `LoadCacheFromFile` with a cache that is stopped,
   being collected, or loaded twice: do they rely on the periodic goroutine or on maintenance
   running?

## For each defect

- The lifecycle transition (construction, stop, collection) and the operation racing with it.
- A concrete scenario: the goroutines, the calls and the lines.
- The observable result: a goroutine leak, a cleanup stuck forever, a deadlock, a lost result.
- A witness under `.local/audits/<model>/audit-lifecycle-evidence/`. For leaks and collection,
  force it with `runtime.GC()` in a loop and observe it with `runtime.NumGoroutine` or a cleanup
  of your own on the `Cache` that must run. Do not observe collection inside a `testing/synctest`
  bubble: the cache's cleanup runs outside it.
