# Design decisions

Behavior that looks wrong but is intentional. Each entry is a mechanism plus its consequence;
it disposes of a finding only when both match. A finding with the same mechanism but a
different consequence (another trigger, configuration or call site) is still live.

Audits read this file **after** recording their findings (Phase 1.5 of the auditor).

## Reads and expiration

- **Expired entries stay in the table until the timer wheel removes them.** Reads check
  `HasExpired` and treat them as absent. The wheel works in buckets of about 1 s at the finest
  level, so removal is late by up to a bucket.
- **The read buffer is lossy.** A dropped access is a lost policy signal, not a bug. Anything
  that must reach the policy (insert, replacement, weight change, earlier deadline) goes
  through the lossless write buffer.
- **`Entry` values can mix fields of different writes of the same key.** The fields are read
  one at a time from a node that is updated in place, as in Caffeine. (The doc comment still
  calls it an immutable snapshot; that is a known documentation gap.)
- **`ExpireAfterRead` deadlines move with a CAS on every read** under `ExpiryAccessing`. No
  tolerance window exists today.

## In-place updates

- **A live entry is updated in place**, so there is one node per key for most of its life.
  Values that do not fit a word are inline until the first update, then boxed: exactly one
  node replacement per key.
- **A write over an expired entry that is still in the table reuses its node** and reports
  the old value with `CauseExpiration`. A trace experiment (7 traces, 3 expiry policies)
  showed the same hit rate as replacing the node, and better than re-admitting it through the
  window.
- **An eviction racing with an in-place update evicts the key with the new value.** The victim
  is a key, not a version, as in Caffeine.
- **`OnAtomicDeletion` runs under the bucket lock before the new value is published**, for
  updates in place as for replacements.
- **Both deletion listeners receive the cause decided under the lock** (`writeResult.cause`,
  `deleteNodeFromMap`'s returned cause).

## Policy

- **Eviction calls `evictNode(n, 0)`**, so an expired node that is evicted for size before the
  wheel reaches it is reported as `CauseOverflow`, where Caffeine reports expiration.
  Known and undecided.
- **Region maxima (`windowMaximum`, `mainProtectedMaximum`) are targets**, adjusted by at most
  `queueTransferThreshold` (1000) transfers per maintenance; the regions can lag behind them.
- **Hashing is randomized per cache**, so hit rates of one simulator run vary by up to ±3
  points on small traces. Compare policies over repeated runs.

## Memory and layout

- **On 32-bit platforms, nodes with a pointer-shaped value and an 8-byte atomic in the header
  are 8 bytes larger** than before the header/layout split. Go cannot place the value in the
  header's padding; a separate header per storage would undo the binary-size win.
- **Every node type is instantiated for every `(K, V)` shape**, because `NewManager` chooses the
  variant at runtime. The variants share one header type to keep that cost small.

## Rejected approaches

- **Seqlock for multi-word values**: torn reads on arm64 (acquire load does not order earlier
  plain loads), a data race under the race detector.
- **`sync.Pool` for nodes or value boxes**: lock-free readers, buffers and iterators may still
  hold the pointer; reuse needs epochs or hazard pointers, which cost every read.
