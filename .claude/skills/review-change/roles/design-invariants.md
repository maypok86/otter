# Role: design invariants

You check that the change keeps the invariants otter's design rests on. Read first:
`.claude/docs/concurrency.md` and `.claude/docs/design-decisions.md`, then the code around
every hunk.

For every change, ask:

1. **Lock order.** `evictionMutex` → hash table bucket lock → singleflight bucket lock, and
   nothing is taken in the reverse order. What runs under each lock, and may it run there?
2. **Node lifecycle.** alive → retired → dead, never backwards; retired under the bucket lock,
   dead under `evictionMutex`; a retired node can still be the key's node for a moment.
3. **Weight accounting.** A node's weight is accounted in the policy if and only if it is
   linked into a queue; `policyWeight` versus `weight`; `reweigh` stays idempotent and
   order-independent.
4. **Write buffer.** Tasks may be replayed in any order; every task the policies need reaches
   the buffer or the inline fallback; the policies tolerate an access replayed before the
   insertion.
5. **In-place updates.** `CanSetValue`, `atomicSet` and the store order a lock-free reader
   relies on.
6. **Notifications.** `OnAtomicDeletion` under the bucket lock; `OnDeletion` submitted by the
   mutation on every path before its task reaches the write buffer; maintenance submits only
   evictions and expirations.
7. **Loading.** The singleflight call lifecycle, `committed`, `refused`, and what a waiter may
   take from a call.
8. **User code on the way.** Every hand-off the cache needs (a task to the write buffer, a
   call's `done`, a lock release) is done on every exit of the code before it: a panic or
   `runtime.Goexit` in a callback, the executor or the clock that runs earlier on the same
   goroutine must not skip it. Moving user code earlier in a path is the usual way to break
   this.
9. **Expiration and time.** Where the clock is read relative to the lock, `expireTolerance`,
   and the timer wheel's idempotent `Schedule`.

Report only what the documents do not explain as intentional. When the change alters an
invariant on purpose, check that `concurrency.md` or `design-decisions.md` is updated with it,
and report it if not.
