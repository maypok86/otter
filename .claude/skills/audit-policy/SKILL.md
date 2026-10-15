---
name: audit-policy
description: Audit the W-TinyLFU eviction policy, the window climber, region resizing and the frequency sketch for implementation defects (not algorithm quality)
context: fork
agent: auditor
disable-model-invocation: true
---

Audit the eviction policy for implementation defects: wrong arithmetic, sign errors, broken
structural invariants, accounting drift, stalls. Adapted from Caffeine's `audit-adaptivity`
for otter's port of its window climber, extended to the rest of the policy.

## Scope: correctness, not tuning

Out of scope: convergence speed, hit rate, oscillation as a trade-off, the values of the
constants, and the climber's algorithm. `climber.go` is a line-by-line port of Caffeine's
`WindowClimber` and follows it (see `design-decisions.md`), so "the climber could converge
faster" or "constant X should be Y" is not a finding here, and neither is a behavior the
reference has. Hit rate is measured with `/sim-compare`, and parity with the reference with
`/sim-parity`, not argued here.

Report a defect when a value is wrong, an invariant breaks, adaptation stalls permanently, the
port departs from the reference (a parity loss), or the policy and the hash table disagree about
which entries exist. A departure from the reference is a finding only with the reference line
it departs from: use the checkout `/sim-parity` makes (`.local/parity/caffeine`), or clone
`https://github.com/ben-manes/caffeine` at the commit the port follows (named in
`.claude/skills/sim-parity/traps/README.md`), and quote `WindowClimber.java`,
`BoundedLocalCache.java` or `FrequencySketch.java`.

## Code in scope

- `climber.go`: `windowClimber` (`resized`, `resetSample`, `discardSample`, `recordHit`,
  `recordMiss`, `determineAdjustment`, `carryOver`), `reactiveClimber`, `densityClimber`
  (`route`, `armProbe`, `walkStep`, `nextStride`, `probeEnding`, `auditEnding`,
  `starvationEnding`, `undoProbe`, `returnToBase`, `keepConfirmedPosition`, `retestReturn`,
  `steer`), and the helpers `climberSample`, `climberReading`, `climberStep`, `probeWalk`,
  `probeLadder`, `auditClock`, `climberAnchor`, `climberRates`
- `policy.go`: `access`, `accessAs`, `reload`, `accessWritten`, `add`, `update`, `updateNode`,
  `makeDead`, `reweigh`, `contains`, `adjustAccounted`, `evictNodes`, `evictFromWindow`,
  `evictFromMain`, `admit`, `reorderProbation`, `climb`, `climberMaximum`,
  `demoteFromMainProtected`, `increaseWindow`, `decreaseWindow`, `setMaximumSize`
- `sketch.go`: `ensureCapacity`, `increment`, `frequency`, `reset`, `sampleSize`
- `mutation.go`: `afterWrite` (which write is a hit, a reload or quiet); `task.go`: `access`
- `cache_impl.go`: `runTask` (task replay, including out-of-order tasks), `onAccess`,
  `maintenance`, `evictNode`, `SetMaximum`
- `internal/deque/linked.go`: the queues the policy links nodes into
- the invariant validator `validateCache` in `invariants_test.go` and
  `climberInvariantViolations` in `climber_test.go`, which it reports (a test of the invariants
  below; check that it checks what it claims)

State, all under `evictionMutex`. Policy: `maximum`, `weightedSize`, `windowMaximum`,
`windowWeightedSize`, `mainProtectedMaximum`, `mainProtectedWeightedSize`; per node
`policyWeight` and `queueType`. Climber: `adjustment`, `sample` (`hits`, `misses`, `windowHits`,
`probationHits`, `previousHitRate`), `dense`, the reactive tier's `step`, and the density
tier's `step`, `walk` with `walking`, `undoRemaining`, `refractoryLeft`, `retreatLeft`,
`starvation` and `audit` (each a `probeLadder`: `rung`, `crashStreak`, `farthest`,
`farthestDown`), `auditClock` (`down`, `waitSamples`, `stillSamples`, `lastWindow`,
`settledRate`), `anchor` (`window`, `rate`, `held`, `freshLeft`, `returning`, `returnLeft`,
`retestClaim`, `settleLeft`, `shortfallStreak`) and `rates` (`smoothed`, `deviation`).
`climberReading` is a per-sample derived view, computed once and read-only. Constants live
next to the type that uses them in `climber.go`; policy constants: `percentMain`,
`percentMainProtected`, `queueTransferThreshold`, `admitHashdosThreshold`.

## Invariants to attack

1. **Accounted iff linked.** A node's `policyWeight` is in `weightedSize` (and in the window or
   protected size for its queue) exactly while it is linked. Can any task order (add after
   update, update after delete, reconcile after eviction, two writes to a new key before
   maintenance) link a node without accounting it, or account a node that is not linked?
2. **Policy ⊆ table, table ⊆ policy after maintenance.** Every alive node in the hash table is
   in exactly one queue once the buffers are drained; no dead or retired node stays linked.
3. **Region sums.** `windowMaximum + mainMaximum == maximum` after every climb and resize;
   no maximum underflows (they are `uint64`, the climber's arithmetic is `int64`: check every
   conversion between them, and `climberMaximum`'s bound); window and protected weighted
   sizes match the queue contents.
4. **Quota accounting in `increaseWindow`/`decreaseWindow`.** With weighted entries, can the
   quota underflow, skip the loop, or over-run it? Does the `queueTransferThreshold` cap leave
   regions half-adjusted so the next climb misreads them? Is the unspent quota given back
   through `carryOver`?
5. **The climber's arithmetic.** Can the hit-rate division see a zero request count? Can the
   step become 0, NaN or infinite and stall adaptation forever? A `float64 → int64` conversion
   out of range is undefined in Go (Java saturates): is every one bounded? Does
   `math.Copysign` pick the intended direction for a zero or negative-zero amount
   (`flooredDescent` relies on `-0`)? The slow-adapt sample period (`samplePeriod` at
   maximum ≤ 512): can `initialStep / magnitude` divide by zero?
6. **The gates in `climb`.** Three paths skip the tier: an uninitialized sketch
   (`resetSample`), a sample shorter than the period, and a cache below half of its maximum
   (`discardSample`, which keeps the previous hit rate). Can a stale `adjustment` from a prior
   cycle be re-applied? The half-full gate is reachable mid-life while the density tier's walk,
   anchor and rates persist: what does each path do to `previousHitRate`, which the reactive
   step, the workload-shift test and the walk's bold driver compare against?
7. **`setMaximumSize` at runtime** (`SetMaximum`): growing and shrinking, to values below the
   current window or protected size, to 0 or 1, and across the tier thresholds (512 and 4096, in
   both directions). `resized` must select the tier and seed its step (a slow-adapt cache opens
   by growing), and leave no walk, anchor or ladder state from the old tier. Does the sketch's
   sample follow the new maximum, and can a table filled at the old pace break `reset` (its
   subtraction is clamped; check the clamp)?
8. **Write ownership in the density tier** — the highest-yield check upstream, where it produced
   two real bugs invisible from a single code path. Enumerate every write to
   `starvation.rung`, `starvation.crashStreak`, `audit.rung`, `audit.crashStreak`,
   `auditClock.waitSamples`, `auditClock.stillSamples`, `anchor.held`, `anchor.freshLeft` and
   check it against its owner, including endings that are not crashes:

   | Owner | Fields |
   |---|---|
   | Observation | `sample` |
   | The active walk | `walk`, `walking`, `undoRemaining` |
   | Starvation retry | `starvation`, `refractoryLeft` |
   | Audit retry and schedule | `audit`, `auditClock` |
   | Goal guard | `anchor`, `rates` |
   | Motion out | the tier's `step`, `adjustment` |

   One cross-write is sanctioned: an audit confirm rewards the starvation ladder
   (`starvation.reward`) and zeroes `refractoryLeft`. An audit's undo leaves the refractory
   alone. Anything else is a finding. The audit clock is a function of the window's position, so
   any layer that moves the window decays `stillSamples`: that coupling is intended.
9. **Walk and anchor invariants.** `1 <= starvation.rung <= 64`; `0 <= refractoryLeft <=
   starvation.rung`; an undo returns exactly to `walk.baseWindow` (`undoRemaining` accounts for
   the truncated command); the below-floor lift cannot exceed the floor;
   `sample.probationHits <= sample.hits - sample.windowHits`; `walk.baseProbationDensity` is
   written only in `armProbe`, non-negative and finite; the probation denominator is
   `max(1, maximum - windowMaximum - mainProtectedMaximum)`, capacities, never occupancy;
   `freshLeft > 0 ⇒ held`, `held ⇒ isPlanted`, `walking ⇒ undoRemaining == 0`,
   `returning ⇒ held`, `waitSamples > 64 ⇒ audit.rung == 64`. `climberInvariantViolations`
   asserts these; a new invariant joins it.
10. **Signals.** Hits are counted when the read buffer is drained (`access`), misses when an
    insert is replayed (`add`) and when a write over an expired entry is replayed (`reload`, or
    `update` with `accessReload`). Region attribution is taken before `reorderProbation` can
    promote the entry. Zero-weight entries earn no climber hits; a refresh completion is quiet.
    Is any event counted twice or never, in a way that biases the climber systematically?
11. **Admission.** `admit` compares sketch frequencies and admits a warm candidate at random
    (`admitHashdosThreshold`). Check the comparison direction, ties, and what happens to the
    rejected node (removed from the table, notified, accounted).
12. **Sketch.** Table sizing for a maximum near 0 and near the integer limits (minimum 256, the
    table and sample caps), `blockMask`, saturation at 15, halving on reset, and `size` after a
    reset and after a lower maximum cuts the sample.
13. **Zero and huge weights.** Zero-weight entries (kept, never evicted for size?) and entries
    heavier than `maximum` (rejected), through add, update, reweigh and reload.

## Not bugs (adjudicated upstream; read `.claude/docs/hill-climber.md` in the reference first)

- The verdict asymmetry: an up-probe is judged against the probation density frozen at the
  arm (rescaled to the sample's length), a down-probe by the average-density sign. Judging
  against live probation is an absorbing false veto: the walk's own demotions enrich it.
- Probation attribution taken before the access promotes the entry: the promoting access counts
  as a probation hit, the next one as protected.
- A zero baseline confirming any probe that earns four times the bar.
- A walk's frozen bases looking stale: they exist only while `walking`, and an ended walk is
  read only by the undo that prices its ending.
- Hits on zero-weight entries are left out of the climber's sample while their misses count, so
  the sampled hit rate can sit below the user-visible one.

## Method notes

- Build witnesses as deterministic tests: a synchronous executor, a non-ticking clock
  (`newNonTickingClock`), and direct calls to `maintenance` and `runTask` under
  `evictionMutex` (see `inplace_test.go` and `policy_test.go` for the patterns), plus
  `validateCache` after each step. For the climber alone, drive `windowClimber` with the
  helpers in `climber_test.go` (`newTestClimber`, `sampleClimber`, `reactiveSample`,
  `injectWalk`) and
  `requireClimberInvariants`.
- Stress the invariants with `TestCache_InvariantsAfterConcurrentLoad`-style runs under
  `-race`, varying weights, maxima and the `SetMaximum` calls, and with `FuzzWindowClimber`.
- When a defect changes which entries are kept, confirm the effect on hit rate with
  `/sim-compare` before rating it above `medium`; a suspected departure from the reference with
  `/sim-parity`.
