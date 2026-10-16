---
name: audit-feature-interaction
description: Look for defects that appear only when two or three features act on the same entry at once (eviction, expiration, refresh, loading, compute, in-place updates, weight changes, listeners)
context: fork
agent: auditor
disable-model-invocation: true
---

# Audit: feature interaction

Most tests and audits exercise one feature at a time. This one takes pairs and triples of
features acting on the same key at the same time, and asks whether the entry, the policies,
the counters and the notifications end up where each feature alone would leave them.

Adapted from Caffeine's `audit-feature-interaction`.

Assume at least one interaction defect exists. If you find none, re-trace the priority pairs
below and explain, for each, why no interleaving breaks it.

## Features

| | Feature | Where |
|---|---|---|
| A | Eviction for size or weight, including zero-weight entries | `policy.go`, `evictNode`, `removeForSize` |
| B | Expiration: fixed or variable, `SetExpiresAfter`, `expireTolerance`, the timer wheel | `deadline.go`, `internal/expiration`, `expireNode` |
| C | Refresh: on a stale read and explicit (`Refresh`, `BulkRefresh`) | `refreshKey`, `bulkRefreshKeys`, `isRefreshBase` |
| D | Loading: `Get`, `BulkGet`, joined waiters, the owner's context | `singleflight.go`, `loadOrJoin`, `commitCall` |
| E | Writes and conditional writes: `Set`, `SetIfAbsent`, the `Compute` family, `Invalidate` | `mutation.go`, `doCompute` |
| F | In-place updates of a node | `atomicSet`, `CanSetValue` |
| G | Weight changes: a weigher that gives a new value another weight | `reweigh`, `policyWeight` |
| H | Listeners: `OnAtomicDeletion` under the bucket lock, `OnDeletion` on the executor | `notifyDeletionFrom`, `publish` |

Run every pair with the default executor and with a caller-runs one: the executor decides
where maintenance and `OnDeletion` run, and several interactions exist only with one of them.
Operations that act on many keys (`InvalidateAll`, iteration, `SaveCacheTo`, `LoadCacheFrom`,
`SetMaximum`) join the triples.

## Priority

From otter's history (the bug patterns in `.claude/agents/auditor.md`):

1. **Loading or refresh with a write or removal (D+E, C+E, D+A, C+A).** A load or refresh that
   started before a write, an invalidation, an eviction or an expiration of the key: can its
   result replace or bring back what that change produced? Check `refused`, `committed`,
   `deleteOnRemoval` and `isRefreshBase` on every path, single and bulk.
2. **Refresh with expiration (C+B).** A refresh of a node that expires while the reload runs,
   a refresh that extends a deadline it should not, a stale read of an expired entry.
3. **In-place updates with eviction or expiration (F+A, F+B).** The policies and the wheel act
   on a node whose value, weight or deadline a writer has just changed without replacing it.
4. **Listeners with anything (H+\*).** The cause and the value of every event, their order per
   key, what a listener may call (it may write the key), and a panic or `runtime.Goexit` in it,
   on both executors.
5. **Weight changes with eviction (G+A).** A weight that changes while the node is a victim
   candidate, a weight set to zero, `weightedSize` against the sum of what the policy holds.

## For each pair

Twenty-eight pairs: (A,B), (A,C), ..., (G,H).

1. Can both act on the same entry at the same time? If one excludes the other (a lock, a
   check, a documented restriction), say how, and move on.
2. Build the most hostile interleaving, goroutine by goroutine.
3. Trace it to the end: the entry's value and deadline, its place in the hash table, the
   policy queues and the wheel, `weightedSize`, the stats, and the events of both listeners.
   Compare with what a sequential order of the two operations would give.

Then pick the three to five triples you judge riskiest, for example eviction with expiration
and a listener that panics, refresh with expiration and an in-place write, a weight change
with eviction and `Compute`, a load with `InvalidateAll` and expiration. Give the reason for
each choice.

## Known behavior

Do not report what `.claude/docs/design-decisions.md` documents as intentional, or the known
exceptions of `audit-linearizability`, unless the consequence differs from the documented one.

## For each defect

- The features involved and the configuration (bound, expiration, refresh, value storage,
  executor).
- The interleaving, with the line of every step.
- The final state that is wrong, and what each feature alone would have left.
- Why the interleaving is legal under the Go memory model.
- A witness under `.local/audits/<model>/audit-feature-interaction-evidence/`, run with
  `-race`. The `cacheSpec` matrix in `cachespec_test.go` (`forEachSpec`) runs a test across the
  configurations. Steer the schedule with a fake clock or a queued executor if needed, then
  confirm the consequence with the default executor and the real clock
  (`finding-taxonomy.md`).
