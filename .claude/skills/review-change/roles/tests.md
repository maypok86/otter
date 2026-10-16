# Role: tests

You check the tests of the change and whether they prove what they claim.

1. **Every fix has a test that fails before it.** For each `fix:` commit in the range, find the
   test that covers it and the path it exercises. Say whether it would fail on the parent of
   the commit, and why; the checker will run it. One fix per commit: a commit that fixes two
   things is a finding.
2. **The test does what its commit says.** When a commit message or a comment claims that a
   test checks something ("the policies replay tasks in any order", "a write that replaces the
   node"), check that the test reaches that case: probe it if needed (count the node
   replacements, the tasks, the configurations that take the path). A claim the test never
   exercises is a finding.
3. **The test checks the behavior, not the implementation it happens to have.** Would it still
   pass if the bug came back in a slightly different form? Does it assert the observable
   result (values, events, sizes), with exact comparisons rather than conditions that cannot
   fail?
4. **Configurations.** Behavior that depends on the configuration (bound, expiration, refresh,
   value storage, executor) is tested across them: the `cacheSpec` matrix in
   `cachespec_test.go` (`forEachSpec`) covers them. A new test that picks one configuration by
   hand where the matrix applies is a finding; so is a spec restricted further than the
   behavior needs (try the wider one). A test rewritten onto the matrix keeps every
   configuration its old version ran: compare the old options (executor, calculators,
   weigher) with the new spec, since the spec's defaults (queued executor, empty population)
   can drop one silently.
5. **Flakiness.** Randomness that is not controlled (hash seeds, `rand`), timing (`time.Sleep`,
   real clocks, goroutine scheduling) and comparisons that depend on them. Estimate how often
   the test would fail by chance.
6. **Invariants.** A change to the policy or the write path runs `validateCache`
   (`invariants_test.go`); a change that can move hit rates names a `/sim-compare` run.
7. **Cost.** A test that adds seconds to `go test -race ./...` without need.

Report the missing scenario, not "more tests".
