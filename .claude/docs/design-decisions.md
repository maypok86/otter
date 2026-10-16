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
- **With the default executor, a drain can wait about 10 ms when every P is busy, and the reads
  in between are dropped.** `go fn()` puts the drain goroutine in the scheduling P's `runnext`,
  and `wakep` only wakes an idle P, so with every P running readers the drain starts when the
  reader is preempted (`forcePreemptNS`, 10 ms). Meanwhile `drainStatus` is `processingToIdle`,
  no other drain is scheduled, and the full read buffer drops accesses with a cheap `Full`
  check. A dedicated maintenance goroutine woken through a channel lands in `runnext` the same
  way, so in Go only a synchronous drain is fast under saturation. Measured on 8 busy threads
  (Ryzen 7 7840H, 2026-10-12): about 60 drains/s at 100% reads with the default executor
  against 25-115K with `Executor: func(fn func()) { fn() }`, but the synchronous one costs
  10-27% ops/s wherever there are reads, for +1.6-2.0 hit-rate points on 3 of 8 eviction
  workloads and none on the rest. Read throughput wins; with an idle P the delay is gone. A
  drain started by the write path is a separate question (it was faster synchronously on pure
  inserts with eviction).
- **`InvalidateAll` replays the write buffer until it is empty, without a bound.** Unlike
  `drainWriteBuffer`, which stops after `maxWriteBufferSize` tasks, the loop in
  `invalidateAllLocked` has no limit, yet it terminates: it is the only consumer and holds
  `evictionMutex`. Writers slower than the loop let it catch up; faster ones fill the buffer,
  retry and then block on `evictionMutex` in `performCleanUp`, so they drop out until the
  buffer is empty. A rate that keeps the buffer non-empty but never full is not stable.
  Measured on an M4 Pro with 1-24 writers inserting without pause (2026-10-11): every call
  returned, the worst after 291 ms, most of it the one-by-one invalidation that follows; one
  call replayed 92,302 tasks, 45 times the buffer's capacity. A bound would leave tasks of
  writes made before the call for a later maintenance, for no measured gain.
- **`Entry` values can mix fields of different writes of the same key.** The fields are read
  one at a time from a node that is updated in place, as in Caffeine. (The doc comment still
  calls it an immutable snapshot; that is a known documentation gap.)
- **A read leaves the deadline as it is when it would move by at most `expireTolerance`**
  (1 s), as long as both the remaining and the new lifetime are longer than that. Otherwise
  every read under `ExpiryAccessing` would CAS the node. Entries can expire up to 1 s early,
  or late when `ExpireAfterRead` shortens the lifetime by less than that. `SetExpiresAfter`
  passes no tolerance and stays exact.
- **A read comes before an in-place update that races with it.** `getNode` loads the value
  once; `ExpireAfterRead` is shown that value and the read returns it. Before its CAS, the read
  checks that the node still stores the same value (`ValueAndIdentity`) and otherwise leaves the
  deadline to the update: the CAS compares only the deadline, and the update may store the very
  deadline the read saw, for example when the clock has not moved. Two cases stay inexact. An
  update in the few instructions between that check and the CAS can still store the same
  deadline, and the new value then gets the read's. An update whose `ExpireAfterUpdate` keeps the
  deadline keeps the one it saw, so the read loses its extension. Exact handling needs a write
  version in every node or the bucket lock on reads. Two other fixes were rejected: comparing
  the values bitwise (Go does not keep padding bytes, so copies of a padded struct differ), and
  undoing the CAS after the fact, which broke updates that keep the deadline and needed a
  write-buffer task that can block a read under a bucket lock.

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

## Loading and refresh

- **A refresh is registered under the key's bucket lock.** Writes cancel calls under that lock,
  so a refresh is either cancelled by a write or registered after it, and a read that saw a
  write never joins a refresh the write cancelled. A refresh that joins a registered call takes
  no lock, but one that registers does. It waits for whatever holds the lock: a `Compute`
  function, a weigher, a calculator or `OnAtomicDeletion` of a key in the bucket, or a growth of
  the table. With an instant loader every stale read registers, and such reads cost about
  19% more on 8 cores; with a 1 ms loader they are no slower. A refresh that a read started
  is skipped if its key is gone by the registration.

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
- **A panic of user code reaches the caller only when that code produces the caller's result.**
  The loader of a `Get`/`BulkGet` (as a `*panicError`), a `Compute` function, the weigher and
  the calculators of the caller's write, `ExpireAfterRead` of the caller's read, the loop body
  of an iteration, `SaveCacheTo`'s writer, `Stats` (the recorder's `Snapshot`) and the executor
  in a manual `Refresh`/`BulkRefresh` panic to the caller, with the locks released and the
  write not applied. An asynchronous result carries the panic as its error instead: a manual
  refresh reports its loader's panic in `RefreshResult.Err`, and a caller that joined another's
  load gets it as the load's error. Observers and the cache's own work log it and carry on:
  the deletion listeners (`OnAtomicDeletion` too, although it runs inside the caller's write),
  the recorder's `Record*`, the logger itself (dropped), and maintenance, a refresh that a read
  starts and the periodic clean-up, including the clock and the executor they call. Maintenance
  counts as the cache's own work even when the caller asks for it (`CleanUp`, `SetMaximum`,
  `Hottest`): a pass also applies the writes of other callers, which a panic would lose. So a
  panicking `Record*` is logged while a panicking `Snapshot` is not, and a panicking executor is
  logged for a stale `Get` but not for `Refresh`: neither pair is an inconsistency.
- **The executor must not block the submitter; a deadlock with one that does is the documented
  contract.** Maintenance submits `OnDeletion` while it holds `evictionMutex`, the drain is
  submitted under it (the token hands the lock to a caller-runs drain), and a drain that runs on
  the executor submits `OnDeletion` from its own slot. A pool that makes the submitter wait for a
  free slot (a semaphore in front of `go fn()`, `errgroup` with `SetLimit`) therefore deadlocks:
  with one slot, the drain waits for the slot it holds (#263). Submitting only after the lock is
  released would not be enough: the drain's own submissions, and those of callbacks that run on
  the executor and call the cache, still come from a slot, and Go cannot tell the cache it is on
  one. A full pool runs the task on the caller instead, as `Options.Executor` shows.

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
- **The hill climber does not move the window of a cache below 16 entries.** It steps by a
  sixteenth of the maximum, and the adjustment truncates the step to whole entries, so below 16
  every adjustment is zero and the window keeps its initial split. A floor of 2 on the step
  makes it move, and costs hit rate: on the simulator at capacities 4 to 16 (5 interleaved runs),
  OLTP lost 0.1 to 1.2 points (2.33% to 1.18% at 15) and Glimpse 0.2 to 0.3, with Mixed
  unchanged. A sample of ten times such a maximum is too noisy for the climber to find a better
  window than the initial one.
- **A load or `Compute` that finds its entry expired but not yet removed counts as a hit in the
  climber's sample.** The caller sees a miss, and the stats record one, but the write lands on
  the existing node, so the policy sees an access; had maintenance removed the entry first, the
  same operation would have been an insertion and a miss. The climber compares the hit rate of
  a sample with the previous one, and a steady share of such hits cancels in that difference.
  Counting them as misses would need a write-buffer task for every reload of an expired entry,
  since the read buffer cannot say that an access was a miss, which undoes part of what writing
  in place saves. The simulator cannot measure it either way: its traces do not expire. Worth
  revisiting with a climber that uses a sample's hit rate on its own rather than its change.

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

- **A write version in every node, recorded by each refresh, to tell an in-place update from an
  unchanged base**: memory in every node for a race that registering refreshes under the bucket
  lock closes without it.
- **Seqlock for multi-word values**: torn reads on arm64 (acquire load does not order earlier
  plain loads), a data race under the race detector.
- **`sync.Pool` for nodes or value boxes**: lock-free readers, buffers and iterators may still
  hold the pointer; reuse needs epochs or hazard pointers, which cost every read.
