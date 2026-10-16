---
name: audit-linearizability
description: Find the linearization point of every public Cache method and try to build a history that no sequential order explains
context: fork
agent: auditor
disable-model-invocation: true
---

# Audit: linearizability

Other audits ask whether the cache stays consistent inside. This one asks only what callers can
observe: for every public method, the single step at which it appears to take effect, and
whether concurrent callers can see results that no sequential order of the operations explains.

Adapted from Caffeine's `audit-linearizability`.

## The sequential model

Judge every history against this model, per key:

- The cache is a map. `Get` on a present key returns its value; on an absent key it calls the
  loader, stores the result and returns it (an error or `ErrNotFound` stores nothing).
- Expiration and eviction are spontaneous removals. An expired entry is absent for every
  operation (`design-decisions.md`), however long it stays in the table.
- `Refresh` and `BulkRefresh` are asynchronous. The refresh is requested when its call is
  registered (on the requesting goroutine, inside `Refresh` or the read that found the entry
  stale), and takes effect when its result is committed. Its result is discarded if the key is
  written or invalidated after the request, or if the node it was requested for expires
  (`cache.go`, `Refresh`). A `RefreshResult` reports what was loaded, even when the cache
  discarded it: it is not a read of the cache.
- Bulk methods (`BulkGet`, `BulkRefresh`, `InvalidateAll`, `All`/`Keys`/`Values`) are judged
  per key. State whether any of them gives more than per-key atomicity.

## Known exceptions

Do not report these again unless the mechanism or the consequence differs:

- `InvalidateAll` while loads are in flight is documented as undefined.
- A read may move an entry's deadline by up to `expireTolerance` without storing it
  (`deadline.go`), so an entry may expire up to that much early or late.
- `Entry` fields may mix two writes of the same key (`design-decisions.md`).

## Methods

Single key: `GetIfPresent`, `GetEntry`, `GetEntryQuietly`, `Get`, `Set`, `SetIfAbsent`,
`Compute`, `ComputeIfAbsent`, `ComputeIfPresent`, `Invalidate`, `SetExpiresAfter`,
`SetRefreshableAfter`, `Refresh`.

Bulk and aggregate: `BulkGet`, `BulkRefresh`, `InvalidateAll`, `All`, `Keys`, `Values`,
`EstimatedSize`, `WeightedSize`, `Hottest`, `Coldest`.

## Step 1: linearization points

For each method:

1. Name the linearization point as a concrete step: the bucket lock in `hashmap.Compute`, the
   lock-free load in `hashmap.Get`, the commit of a load in `commitCall`, a CAS. Give file and
   function.
2. If it depends on the case, enumerate the cases. For `Get` at least: a hit; a stale hit
   during a refresh; a miss that loads; a miss that adopts a value committed meanwhile; a miss
   that finds a value written during its load; a waiter of a load, of a refresh, of a refresh
   whose result was refused (`call.refused`), of a load whose value had expired before the
   waiter started (`call.expiresAt`), of a load that failed for its owner's context.
3. Give a two-goroutine scenario that confirms the point.

## Step 2: attack

Try to build violations. At least:

- **Order**: `Set(k, v1)`, `Set(k, v2)` and a reader: can a reader see v2, then v1?
- **Lost write**: can a load, a refresh or an extra `BulkLoad` entry replace or remove a value
  that a write or `Invalidate` produced after the load or refresh was requested?
- **Conditional writes**: `SetIfAbsent`, `ComputeIfAbsent` and `Compute` that saw the key
  absent, against a concurrent `Get` that loads it. Both cannot be right about absence.
- **Values that were never cached**: can `Get` return a value that was never live in the cache
  during its call (a discarded refresh, a value that expired before it started, a value loaded
  for an invalidated key)?
- **Invalidate then read**: after `Invalidate(k)` returns, can any read return the invalidated
  value or a value derived from it by `Reload`?
- **Single flight**: can the loader run twice for the same key while a call for it is
  registered, and can two waiters of one call get different results?
- **Lock-free reads of in-place updates**: a node's value, expiration and refresh times are
  separate atomic fields. Can a reader take an old value with a new deadline, or the reverse,
  and return a value that had expired, or report a live entry as absent? Trace the store order
  in `atomicSet` and the load order in `getNode`, `GetEntry` and the iterators.
- **Size and aggregates**: is `EstimatedSize` documented as an estimate? Bound the error.

## For each candidate violation

- The full interleaving, with the goroutines, the calls and the lines where each step happens.
- The sequential history it violates: why no order of the operations explains every result.
- Why the interleaving is legal under the Go memory model: name the synchronizing operations.
- For a time-based violation (an entry expiring early or late), a goroutine that observes the
  key inside the window. Without one, an operation can linearize anywhere in its interval,
  including at its own clock read, and the history is legal. Exclude configurations that allow
  other removals (`MaximumSize`, `MaximumWeight`), since they also explain a miss.
- A witness under `.local/audits/<model>/audit-linearizability-evidence/`, run with `-race`.

Do not report internal inconsistencies that no caller can observe.
