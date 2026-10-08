---
name: audit-contract-drift
description: Find places where the documented API contract and the implementation diverge
context: fork
agent: auditor
disable-model-invocation: true
---

Most audits read code. This one reads the documentation first and then traces each promise to
every path that should keep it. It finds quiet contradictions between what the doc comments
say and what the code does.

## Step 1: enumerate the promises

Read every exported doc comment and build a list of behavioral promises (exact wording, the
configurations that activate it, the operations it covers):

- `cache.go`: every method of `Cache`, especially words like "must", "will", "never",
  "always", "returns", "if the key is not present"
- `options.go`: each option, its validation and its interaction with others
- `entry.go`: what an `Entry` is and when its fields are consistent
- `deletion.go`: each `DeletionCause` and when it is reported; `OnDeletion` vs
  `OnAtomicDeletion`
- `loader.go`, `expiry_calculator.go`, `refresh_calculator.go`: what the cache passes to user
  code and what it does with the results and errors (`ErrNotFound`, panics)
- `stats/`: what each counter counts
- `persistence.go`: what survives a save and load
- `README.md` and `docs/` claims about behavior and guarantees

## Step 2: trace each promise

For each promise, follow every path that should honor it: single-key and bulk calls,
`Compute*`, loads and refreshes (manual and automatic), expiration found by the wheel or by a
read, eviction, in-place updates and replacements, resurrection of expired entries, iterators
and snapshots, persistence round-trips, caches with and without maintenance.

A path that does something other than what the comment promises is a drift finding. So is a
promise that only some paths keep.

Patterns to check explicitly:

- **"expired entries are never visible"**: paths that revive or return an expired entry
  (`SetExpiresAfter`, `Invalidate`'s return value, `Set` over an expired entry, iterators)
- **Return values on expired-but-present keys**: `Set`, `SetIfAbsent`, `Compute`,
  `Invalidate` report "present" or "absent" consistently with reads
- **Deletion causes**: each cause matches its documented meaning on every path that reports it
  (eviction of an already expired node, load and refresh completions reported as replacement)
- **Snapshots**: `Entry` described as consistent while fields are read one at a time
- **Stats**: hits, misses, load successes and failures counted as documented (e.g. whether
  `ErrNotFound` is a failure)
- **Bulk partial results**: what `BulkGet`/`BulkRefresh` promise for missing and extra keys
- **Persistence**: remaining lifetimes and stats after `LoadCacheFrom`
- **Callbacks**: what the docs say about re-entrancy and the goroutine a callback runs on

Each finding anchors both sides: the contract (file and quoted comment) and the divergent
implementation (file and function), plus a minimal user-visible scenario where they disagree.
Say which side should change, if the evidence decides it.
