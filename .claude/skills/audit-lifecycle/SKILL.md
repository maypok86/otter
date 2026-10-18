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
goroutine and `StopAllGoroutines` against every place that can block, and explain why
neither can block the other.

## The lifecycle

- `New` (`cache.go`) builds the internal `cache` (`newCache`, `cache_impl.go`) and wraps it in
  `Cache`.
- With expiration, `newCache` starts `periodicCleanUp`, which holds the cache through a
  `weak.Pointer` and runs `CleanUp` on every tick of `Clock.Tick` until the cache's `stop`
  channel is closed or, at a tick, the cache is gone; it closes `done` when it returns, at once
  if `Tick` gives no ticks. A `runtime.Goexit` in user code that a clean-up runs ends the
  goroutine, and its defer starts a new one with the same ticks, so `done` is still closed once;
  a Goexit in `Tick` itself ends the periodic clean-ups. No cleanup is registered: cleanups run
  outside `testing/synctest` bubbles, where touching a bubbled channel is a fatal error.
- `StopAllGoroutines` closes `stop` once and waits for `done`, and is documented as forbidden
  inside functions the cache runs.
- Other goroutines: the default executor (`go fn()`), the goroutines that wait for a joined
  load or refresh and its successor (`refreshKey`, `sendAfterJoined`, `joinedResult`), the
  goroutine `group.finish` starts to submit the refresh registered in place of a joined load
  that failed for its owner's context (`loadAgain`, the `again` closures of `refreshKey` and
  `bulkRefreshKeys`), and whatever a custom executor starts.
- `NewCoarseClock` (`clock.go`) starts a goroutine that belongs to the clock, not to a cache:
  it holds the clock through a `weak.Pointer`, stores the time on every tick of its ticker, and
  returns when the clock is gone or `Stop` closes `stop`. `StopAllGoroutines` does not stop it.
  After `Stop`, `NowNano` reads the precise time.

## Questions

1. **Reachability.** A goroutine's stack is a root: if the `Cache` is reachable from it, the
   `Cache` is never collected. The internal `cache` holds the user functions from the options
   (listeners, calculators, the weigher, the executor, the logger, the stats recorder, the
   clock), and one of them may capture the `Cache`. Check that the periodic goroutine holds
   nothing between ticks that reaches the internal `cache`, and that no other goroutine the
   cache starts holds it longer than its task. A clock that does not tick leaves the goroutine
   behind a collected cache: is that documented?
2. **Blocking.** `StopAllGoroutines` waits for the goroutine, which may be inside `CleanUp`:
   waiting for `evictionMutex` (held by a `Hottest` loop or a long maintenance pass), running
   `OnDeletion` on a synchronous executor, or in a slow clock. `Clock.Tick` is called once,
   before the loop, while the goroutine holds the cache: can a `Tick` that does not return keep
   `StopAllGoroutines` blocked and the cache alive forever?
3. **Stop from inside the cache.** `StopAllGoroutines` called from `OnDeletion` on a synchronous
   executor during a periodic `CleanUp` deadlocks: it waits for `done`, which only the goroutine
   running the listener closes, when it returns. It is documented
   as forbidden. Find the paths where user code runs on the periodic goroutine without the user
   choosing a synchronous executor, if any, and other functions that run there that the
   warning does not name.
4. **Operations after stop.** After `StopAllGoroutines`, does every operation still work, with
   expired entries removed only by later operations? Does a second call return false, and do
   concurrent calls stop the goroutine exactly once? Is
   anything else (refresh waiters, executor tasks) left running that the name promises to stop?
5. **Operations during collection.** Can the goroutine find the cache gone while an operation is
   still in progress on the `Cache`, for example after the caller's last use of the wrapper
   inside a long `Get`, `Compute` or iteration? Once the goroutine has returned, does anything
   still expect periodic clean-ups? A cache with expiration created inside a `testing/synctest`
   bubble owns bubbled channels: does anything outside the bubble touch them when such a cache
   is collected before `StopAllGoroutines`?
6. **Construction.** `newCache` starts the goroutine before `SetMaximum` runs. Can anything
   after it panic and leak the goroutine?
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
- The observable result: a goroutine leak, a call stuck forever, a deadlock, a lost result.
- A witness under `.local/audits/<model>/audit-lifecycle-evidence/`. For leaks and collection,
  force it with `runtime.GC()` in a loop and observe it with `runtime.NumGoroutine` or a cleanup
  of your own on the `Cache` that must run. Inside a `testing/synctest` bubble, let the fake
  clock reach a tick after the collection, and keep in mind that cleanups run outside it.
