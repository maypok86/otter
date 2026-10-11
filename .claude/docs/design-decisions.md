# Design decisions

Behavior that looks wrong but is intentional. Each entry is a mechanism plus its consequence;
it disposes of a finding only when both match. A finding with the same mechanism but a
different consequence (another trigger, configuration or call site) is still live.

Audits read this file **after** recording their findings (Phase 1.5 of the auditor).

## Reads and expiration

- **Expired entries stay in the table until the timer wheel removes them.** Reads check
  `HasExpired` and treat them as absent. The wheel works in buckets of about 1 s at the finest
  level, so removal is late by up to a bucket.
- **An expired entry is absent for every operation, not only for reads.** `Set` and
  `SetIfAbsent` report no previous value, `Invalidate` reports the key as not present,
  `Compute` gets `found=false`, and `SetExpiresAfter` leaves it alone. Only the listeners see
  its value, with `CauseExpiration`, also when size eviction reaches it before the timer wheel
  does. A write or load over it creates the entry for both
  calculators (`ExpireAfterCreate`, `RefreshAfterCreate`), which see no expiration or refresh
  time, as for an absent key; a refresh whose entry expires while it is in flight is discarded.
- **The read buffer is lossy.** A dropped access is a lost policy signal, not a bug. Anything
  that must reach the policy (insert, replacement, weight change, earlier deadline) goes
  through the lossless write buffer. One exception: a read whose `ExpireAfterRead` moves the
  deadline earlier only offers the node to the read buffer, which keeps write-buffer traffic
  off the read path. If the access is dropped, the entry is already invisible to reads but is
  removed at its former deadline. `SetExpiresAfter` sends an earlier deadline through the
  write buffer.
- **`Entry` values can mix fields of different writes of the same key.** The fields are read
  one at a time from a node that is updated in place, as in Caffeine. (The doc comment still
  calls it an immutable snapshot; that is a known documentation gap.)
- **A read leaves the deadline as it is when it would move by at most `expireTolerance`**
  (1 s), as long as both the remaining and the new lifetime are longer than that. Otherwise
  every read under `ExpiryAccessing` would CAS the node. Entries can expire up to 1 s early,
  or late when `ExpireAfterRead` shortens the lifetime by less than that. `SetExpiresAfter`
  passes no tolerance and stays exact.

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
- **Both deletion listeners receive the cause decided under the lock** (`mutation.cause`,
  `deleteNodeFromMap`'s returned cause).

## Policy

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
- **The read buffer pads small objects with whole cache lines**: `Striped`, the table header
  `striped`, the table of stripes (`newBuffers`) and the space before a ring's `head`. Every
  read loads them, and without the padding each one shares a cache line with whatever the
  allocator put next to it. A neighbor written often (in the throughput benchmark,
  `testing.PB`) made reads of a cache instance about 1.6x slower, so throughput was bimodal
  between instances. The padding costs about 400 bytes per cache plus 64 bytes per
  ring, at most 2.4 KB with 32 stripes.

## Rejected approaches

- **Seqlock for multi-word values**: torn reads on arm64 (acquire load does not order earlier
  plain loads), a data race under the race detector.
- **`sync.Pool` for nodes or value boxes**: lock-free readers, buffers and iterators may still
  hold the pointer; reuse needs epochs or hazard pointers, which cost every read.
