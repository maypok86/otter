---
name: audit-arithmetic
description: Audit for arithmetic and boundary bugs (overflow, underflow, off-by-one, drift)
context: fork
agent: auditor
disable-model-invocation: true
---

Assume there is at least one arithmetic or boundary bug.

Audit for:

- Off-by-one errors and wrong inequality direction (`<` vs `<=`)
- Overflow and underflow, especially **unsigned** subtraction: most sizes, maxima, weights and
  wheel times are `uint64`/`uint32`; a negative difference wraps to a huge value
- Conversions: `int64` ↔ `uint64` time values, `float64` → integer casts (out-of-range is
  implementation-defined in Go; NaN), `uint32` weights summed into `uint64`
- Rounding and truncation in percentages (`percentMain`, `percentMainProtected`, the climber
  step) and in `xmath.RoundUpPowerOf2`
- Sentinel collisions: `unreachableExpiresAt`, `unreachableRefreshableAt`, `noTime`, a zero
  weight, a zero `nowNano` (eviction passes 0)
- Counter drift over a long-running cache: `weightedSize`, `windowWeightedSize`,
  `mainProtectedWeightedSize`, sketch `size` and reset
- Timer wheel bucket arithmetic (`internal/expiration`): spans, shifts, `findBucket`,
  wrap-around of ticks, deadlines behind the wheel's clock
- Frequency sketch: table size, `blockMask`, counter saturation at 15, halving on reset,
  `sampleSize`
- Hill climber: `stepSize`, `adjustment`, quota in `increaseWindow`/`decreaseWindow`, maxima
  that must stay non-negative and sum to `maximum`
- Weight changes made in place (`weight` vs `policyWeight`, `reweigh`), and weights near
  `math.MaxUint32`
- 32-bit platforms (`GOARCH=386`, `arm`): `int` is 32 bits, `atomic.Int64` alignment

For each suspected issue:

- give a concrete input (key, value, weight, clock values, maximum);
- show the evaluation step by step through the code;
- show expected versus actual behavior;
- if it needs billions of operations, say how many.

Ignore style and performance. Report only defects backed by an evaluation trace.
