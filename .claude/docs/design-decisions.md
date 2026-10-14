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
  `SetIfAbsent` report no previous value, `Invalidate` reports the key as not present, `Compute`
  gets `found=false`, and `SetExpiresAfter` leaves it alone. Only the listeners see its value,
  with `CauseExpiration`, also when size eviction reaches it before the timer wheel does, and
  `Stats` counts it as an eviction whichever operation removes it, an `Invalidate` included: the
  count must not depend on whether the wheel or a write gets there first. A write or load over
  it creates the entry for both calculators (`ExpireAfterCreate`, `RefreshAfterCreate`), which
  see no expiration or refresh time, as for an absent key; a refresh whose entry expires while
  it is in flight is discarded.
- **A mutation decides whether the entry is live once, with the time it reads under the bucket
  lock.** The callbacks it then runs under the lock (the `Compute` function, the weigher, the
  calculators, `OnAtomicDeletion`) do not change that decision: an entry that expires while they
  run is still treated as live, although a reader may find the key absent in the meantime.
  Deciding again afterwards would mean calling the `Compute` function a second time. A read is
  checked the same way: its `ExpireAfterRead` CAS is refused if the entry expired while the
  calculator ran, but not if it expires in the few instructions between that check and the CAS.
  A read that checks liveness with the time it read before looking the key up is linearizable:
  the node it finds was live at that time or when it was written.
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

## Callbacks and locks

- **A callback that runs under one of the cache's locks must not call back into the cache,
  except to read single entries.** The bucket locks and `evictionMutex` are plain `sync.Mutex`,
  and `Compute*` functions, the weigher, the calculators and `OnAtomicDeletion` run under the
  bucket lock to stay atomic with the write; the loop body of `Hottest`/`Coldest`,
  `SaveCacheTo`'s writer, `RecordEviction` in maintenance and `OnDeletion` on a caller-runs
  executor run under `evictionMutex`. A deadlock from such a callback that writes (any key),
  loads, iterates or calls a method that takes `evictionMutex` is the documented contract
  (the docs of each callback, of `Hottest`/`Coldest` and of `Options.Executor`), not a bug. A deadlock from a single-entry read
  (`GetIfPresent`, `GetEntry`, `GetEntryQuietly`, `EstimatedSize`) with an executor that runs
  tasks on other goroutines is a bug. With a caller-runs executor such a read may run
  maintenance inline under the held bucket lock, which is documented on `Options.Executor`.

## Policy

- **Region maxima (`windowMaximum`, `mainProtectedMaximum`) are targets**, adjusted by at most
  `queueTransferThreshold` (1000) transfers per maintenance; the regions can lag behind them.
- **Hashing is randomized per cache**, so hit rates of one simulator run vary by up to ±3
  points on small traces. Compare policies over repeated runs.
- **The frequency sketch keeps its table and sample when the maximum shrinks.** The sample
  (ten times the maximum the table was sized for) is both the aging period of the counters and
  the length of a hill-climber sample, so after `SetMaximum` lowers the maximum both run at the
  old pace: from 100,000 to 1000 entries, the climber's first adjustment comes a million
  requests later, and until then the regions keep their initial split. Resizing the sample
  alone is not enough: `reset` subtracts the number of odd counters, which a table filled at
  the old pace can hold more of than a cut `size` allows, and the subtraction would wrap and
  stop the aging for good. A lower maximum is rare, and the split it starts from is safe.

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
