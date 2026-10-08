---
name: audit-sibling-divergence
description: Differential audit of code paths that should behave identically (single vs bulk, equivalent APIs, in-place vs replacement writes, removal paths, node variants, views). Spawns one auditor per group and requires a concrete witness for every divergence.
argument-hint: "[group letters to run, default: all]"
context: fork
disable-model-invocation: true
allowed-tools: Read, Grep, Glob, Bash, Agent, Write
---

# Audit: sibling divergence

Other audits ask whether one path is correct. This one takes two paths that should produce
the same observable result and asks whether they agree. Several otter bugs were exactly this:
`BulkGet` returning zero values where `Get` reports not-found, the two deletion listeners
getting different causes, `Get` and `GetIfPresent` disagreeing during a refresh.

Heavyweight: one agent per group. Not for routine pre-commit review.

Adapted from Caffeine's `audit-sibling-divergence`.

## Input

$ARGUMENTS — group letters to run (e.g. `A C`); all groups if empty.

## Step 1: confirm the inventory

Check that each pair still exists and add new ones (a new API, a new node variant):

**Group A: single key vs bulk**
- A1: `Get(k, loader)` ×N vs `BulkGet(keys, bulkLoader)`: present, missing, error, panic,
  extra keys returned by the loader, stats, waiters joining a load
- A2: `Refresh(k)` vs `BulkRefresh(keys)`: results delivered, not-found handling, extra keys
- A3: `Invalidate(k)` ×N vs `InvalidateAll()`: listener causes, stats, pending loads

**Group B: equivalent by construction**
- B1: `Set(k, v)` vs `Compute(k, … WriteOp)`
- B2: `SetIfAbsent(k, v)` vs `ComputeIfAbsent(k, …)`
- B3: `Invalidate(k)` vs `Compute(k, … InvalidateOp)`
- B4: `GetIfPresent(k)` vs `GetEntry(k)` vs `Get(k, loader)` on a hit, and `GetEntryQuietly`
- B5: `ComputeIfPresent` vs `Compute` on a present and on an expired-but-present key
Compare return values, hit/miss stats, listener events, deadlines and refresh times.

**Group C: write paths that should be indistinguishable**
- C1: update in place vs replacement of an inline node vs resurrection of an expired node
  (`atomicSet`): events, causes, weight accounting, deadlines, refresh scheduling
- C2: `set` vs `doCompute` vs `afterDeleteCall` (load or refresh completion), each calling
  `atomicSet`: what each passes in, what each does after the lock
- C3: `afterWrite` vs `afterDelete` vs `runTask`: which events reach `OnDeletion` and with
  which cause

**Group D: removal paths**
- D1: `Invalidate` vs expiration (`expireNode`) vs eviction (`evictNode`) vs `Compute`
  `InvalidateOp` vs a loader returning `ErrNotFound`: cause, `OnAtomicDeletion` /
  `OnDeletion` agreement, eviction stats, pending loads and refreshes
- D2: expiration found by the timer wheel vs by a read (`HasExpired`) vs by a write over the
  entry

**Group E: storage variants**
- E1: node variants `Inline`, `Boxed`, `P`, `U64`, `Empty` and the stateless types: for every
  `Node` method emitted by `cmd/generator`, the same semantics across variants and feature
  sets (read the emitter and the generated output)
- E2: a cache with maintenance vs one without (no size, weight or expiration): the same public
  results for the same calls

**Group F: views and snapshots**
- F1: `All`/`Keys`/`Values` vs `GetIfPresent` per key; `EstimatedSize` vs counting `All`
- F2: `Hottest`/`Coldest` vs the policy queues
- F3: `SaveCacheTo` + `LoadCacheFrom` vs the original cache: values, remaining lifetimes,
  stats, policy order

## Step 2: one differential auditor per group

Launch one `auditor` sub-agent per selected group, in one message so they run in parallel.
Each writes `.local/audits/<model>/audit-sibling-divergence-group<letter>.md`, never the
canonical report (Step 5 writes that). Prompt:

```
You are auditing otter for sibling divergence: two code paths that should produce the same
observable behavior but do not.

YOUR GROUP: <letter and pairs>

Phase 0: for each pair, state the joint contract (what a caller sees either way), and predict
the 2–3 most likely kinds of divergence.
Phase 1: read both paths end to end. Build a side-by-side table of observable steps: locks,
callbacks invoked, events and causes, stats recorded, return values, fields written. Record
every difference BEFORE reading .claude/docs/design-decisions.md.
Phase 2: for each observable, unexplained difference, build a concrete witness: configuration,
call sequence, expected result if the paths agreed, actual result. Write it as a Go test in a
scratch copy of the repository (rsync -a --exclude .git) and run it with -race. No witness, no
finding.
Phase 3: try to refute each finding by re-reading the source. Drop what you cannot defend.
Output per finding: PAIR, PATH-A (file:function), PATH-B, DIVERGENCE, WITNESS (and whether it
ran and failed), OBSERVABLE, DESIGN-MATCH (entry in design-decisions.md or "none"),
SEVERITY, CONFIDENCE. With zero findings, list every pair inspected, every function traced and
every difference dismissed with its reason.
Do not report performance, style or comment differences.
```

## Step 3: evaluator challenge

For each group report, spawn one `general-purpose` evaluator that sees only the report: is
each witness reproducible as written (unstated preconditions)? Is the divergence observable
to a caller? Is the assumed joint contract stronger than the documentation? Which kinds of
divergence did a zero-finding group under-weight (panics, expired-but-present entries,
in-flight loads, weighted caches, 32-bit)? Send the challenges back to the group's auditor and
keep only findings it defends with evidence.

## Step 4: adjudicate

Read `.claude/docs/design-decisions.md`. Classify each surviving finding as
**confirmed-divergence** (a witness violates the joint contract), **intentional-divergence**
(a documented decision, kept under "explained"), or **documentation-gap**.

## Step 5: report

Write `.local/audits/<model>/audit-sibling-divergence.md`:

```
# Sibling divergence audit
[N] auditors, [M] pairs, [G] groups. [K] findings survived the challenges.

## Confirmed divergences
#1 [severity] [group/pair]: one-line summary
- PATH-A / PATH-B / DIVERGENCE / WITNESS / OBSERVABLE

## Intentional divergences
## Documentation gaps
## Coverage per group
## Evaluator challenges and outcomes
## Residual risk
```
