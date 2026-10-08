---
name: audit-policy
description: Audit the W-TinyLFU eviction policy, the hill climber, region resizing and the frequency sketch for implementation defects (not algorithm quality)
context: fork
agent: auditor
disable-model-invocation: true
---

Audit the eviction policy for implementation defects: wrong arithmetic, sign errors, broken
structural invariants, accounting drift, stalls. Adapted from Caffeine's `audit-adaptivity`
for otter's reactive climber, extended to the rest of the policy.

## Scope: correctness, not tuning

Out of scope: convergence speed, hit rate, oscillation as a trade-off, the values of the
constants. Do not report "the climber could converge faster" or "constant X should be Y". Hit
rate is measured with `/sim-compare`, not argued here.

Report a defect when a value is wrong, an invariant breaks, adaptation stalls permanently, or
the policy and the hash table disagree about which entries exist.

## Code in scope

- `policy.go`: `access`, `add`, `update`, `updateNode`, `delete`, `makeDead`, `reweigh`,
  `isLinked`, `adjustAccounted`, `evictNodes`, `evictFromWindow`, `evictFromMain`, `admit`,
  `reorderProbation`, `climb`, `determineAdjustment`, `demoteFromMainProtected`,
  `increaseWindow`, `decreaseWindow`, `setMaximumSize`
- `sketch.go`: `ensureCapacity`, `increment`, `frequency`, `reset`, `sampleSize`
- `cache_impl.go`: `runTask` (task replay, including out-of-order tasks), `onAccess`,
  `maintenance`, `evictNode`, `removeNode`, `SetMaximum`
- `internal/deque/linked.go`: the queues the policy links nodes into
- the invariant validator `validateCache` in `invariants_test.go` (a test of the invariants
  below; check that it checks what it claims)

State, all under `evictionMutex`: `maximum`, `weightedSize`, `windowMaximum`,
`windowWeightedSize`, `mainProtectedMaximum`, `mainProtectedWeightedSize`, `stepSize`,
`adjustment`, `hitsInSample`, `missesInSample`, `previousSampleHitRate`; per node
`policyWeight` and `queueType`. Constants: `percentMain`, `percentMainProtected`,
`hillClimberStepPercent`, `hillClimberStepDecayRate`, `hillClimberRestartThreshold`,
`queueTransferThreshold`, `admitHashdosThreshold`.

## Invariants to attack

1. **Accounted iff linked.** A node's `policyWeight` is in `weightedSize` (and in the window or
   protected size for its queue) exactly while it is linked. Can any task order (add after
   update, update after delete, reconcile after eviction, two writes to a new key before
   maintenance) link a node without accounting it, or account a node that is not linked?
2. **Policy ⊆ table, table ⊆ policy after maintenance.** Every alive node in the hash table is
   in exactly one queue once the buffers are drained; no dead or retired node stays linked.
3. **Region sums.** `windowMaximum + mainMaximum == maximum` after every climb and resize;
   no maximum underflows (they are `uint64`); window and protected weighted sizes match the
   queue contents.
4. **Quota accounting in `increaseWindow`/`decreaseWindow`.** With weighted entries, can the
   quota underflow, skip the loop, or over-run it? Does the `queueTransferThreshold` cap leave
   regions half-adjusted so the next climb misreads them? Is the unspent quota given back?
5. **`determineAdjustment`.** Can the hit-rate division see a zero request count? Can
   `stepSize` become 0 or NaN and stall adaptation forever (for example at small maxima where
   `0.0625 * maximum` truncates to 0 when converted to `int64`)? Is a stale `adjustment`
   re-applied when the sample is incomplete or the sketch uninitialized?
6. **`setMaximumSize` at runtime** (`SetMaximum`): growing and shrinking, including to values
   below the current window or protected size, and to 0 or 1. Do the maxima, the step and the
   sample stay consistent? Does the sketch get resized, and does `sampleSize` follow?
7. **Signals.** Hits are counted when the read buffer is drained (`access`), misses when an
   insert is replayed (`add`). Which writes count as what: in-place updates (an access),
   replacements, resurrections of expired nodes, load and refresh completions? Is any event
   counted twice or never, in a way that biases the climber systematically?
8. **Admission.** `admit` compares sketch frequencies and admits a warm candidate at random
   (`admitHashdosThreshold`). Check the comparison direction, ties, and what happens to the
   rejected node (removed from the table, notified, accounted).
9. **Sketch.** Table sizing for a maximum near 0 and near the integer limits, `blockMask`,
   saturation at 15, halving on reset, and `size` after a reset; behavior when `ensureCapacity`
   is called with a smaller capacity.
10. **Zero and huge weights.** Zero-weight entries (kept, never evicted for size?) and entries
    heavier than `maximum` (rejected), through add, update and reweigh.

## Method notes

- Build witnesses as deterministic tests: a synchronous executor, a fake clock, and direct
  calls to `maintenance` and `runTask` under `evictionMutex` (see `inplace_test.go` and
  `policy_test.go` for the patterns), plus `validateCache` after each step.
- Stress the invariants with `TestCache_InvariantsAfterConcurrentLoad`-style runs under
  `-race`, varying weights, maxima and the `SetMaximum` calls.
- When a defect changes which entries are kept, confirm the effect on hit rate with
  `/sim-compare` before rating it above `medium`.
