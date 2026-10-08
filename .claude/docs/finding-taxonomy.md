# Finding taxonomy

Shared classification for audit and review findings. Adapted from Caffeine's
`.claude/docs/finding-taxonomy.md`.

## Severity

| Level | Definition | Example |
|---|---|---|
| critical | Wrong result or corruption under a reachable interleaving on a default configuration | lost update, a node left in the table but outside the policy |
| high | Contract violation under specific but supported conditions | a bucket left locked after a recovered panic in a user callback |
| medium | Correct today, but only because of an incidental detail; or a narrow window | a guarantee that holds only because one caller checks first |
| low | Robustness or documentation concern | a doc comment that promises more than the code keeps |

State the affected configuration separately from the harm within it. A non-default
configuration does not cap the severity.

## Categories

| Category | Scope |
|---|---|
| memory-model | atomics, `unsafe` conversions, publication without happens-before (Go memory model) |
| state-corruption | weight or size drift, lifecycle violations, a node in the wrong queue or in no queue |
| specification | the public API contract in `cache.go` and friends is broken |
| notification | missing, duplicated or misattributed `OnDeletion` / `OnAtomicDeletion` events |
| panic-safety | a panic leaves locks held, calls pending or state half-committed |
| liveness | deadlock, stranded drain, a waiter that never returns |
| arithmetic | overflow, underflow, truncation, off-by-one, sign errors |
| api-surprise | a valid call returns a value nobody would expect |
| performance | avoidable allocation, atomic or contention on a hot path |
| policy | eviction policy or climber implementation defect (not tuning) |

## Severity is priced on a realistic configuration

A mechanism you can see and an impact a user can reach are two separate claims; severity
encodes the second. Test instruments can manufacture impacts:

- **A frozen fake clock.** Several mechanisms self-heal because time moves. Re-check with the
  real clock before rating `high`.
- **A synchronous executor** (`Executor: func(fn func()) { fn() }`). It turns asynchronous
  listener and refresh work into re-entrant calls under the caller's locks. Use it to
  control scheduling, then confirm the impact with the default executor.

## Confidence

| Level | Meaning |
|---|---|
| high | A concrete interleaving or input is constructed; a test reproduces it |
| medium | Plausible, but depends on timing or conditions not fully verified |

Omit low-confidence speculation.

## Triage labels

| Label | Meaning |
|---|---|
| patch | real issue, fix in the current change |
| defer | real issue, pre-existing; separate `fix:` commit |
| reject | false positive or explained by `design-decisions.md` |
| escalated | cannot be settled statically; needs a stress or race test |
