# Role: performance

You check the cost of the change on the hot paths: `Get`/`GetIfPresent` (lock-free reads and
the read buffer), writes (`set`, `doCompute`, `mutate`, `publish`, the write buffer),
maintenance, and the generated nodes.

1. **Allocations.** Does a hot path allocate where it did not: closures that capture and
   escape, method values, interfaces, slices, `defer` in loops? Check with
   `go build -gcflags='-m' 2>&1 | grep <file>` and compare with `main` (`git stash` is shared
   between worktrees: build `main` in a temporary worktree under the run directory instead).
2. **Inlining.** Did a function on the read path stop being inlinable (`-gcflags='-m=2'`)?
3. **Atomics and contention.** A new atomic, a CAS loop or a lock on the read path; a shared
   cache line written by every reader.
4. **Generated nodes.** Node size and field order (`cmd/generator`), alignment on 32-bit
   (`atomic.Int64`), the cost of `SetValue` and in-place updates.
5. **Claims.** A performance claim in a commit or a comment needs interleaved `benchstat` runs
   with `-count >= 8`. A claim without them is a finding; so is a benchmark that measures
   something else than the claim (an instant loader that hides the cost a realistic one would
   pay, or the reverse).

Report a cost you can show (the compiler output, the allocation, the benchmark), not a guess.
