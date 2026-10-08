# Audit rounds

Read when starting or triaging a batch of `/audit-*` runs.

- **Finish the batch before repairing.** Consolidate the reports (see `audit-output.md`), then
  work one item at a time, and re-run the audits a fix touches.
- **Run audits one after another.** Hitting a quota mid-run breaks an audit instead of pausing
  it. Order the batch by expected value so an early stop keeps the most findings.
- **A report row is a claim, not a confirmed defect.** In Caffeine's experience only a small
  fraction of claims survive source review. Every claim gets an independent check: a
  `general-purpose` agent re-reads the code and, for `high` or `critical`, builds a failing
  test. Use `general-purpose`, not `auditor`, for verification and consolidation: the auditor
  always writes its own report.
- **A different model on the same skill finds more than the same model twice.**
- **Pre-existing bugs are separate work.** A finding that exists on `main` becomes its own
  branch from `main` and a `fix:` commit with the failing test.

Expected value, highest first, for otter: `audit-exception-safety`, `audit-sibling-divergence`,
`audit-policy`, `audit-reentrancy`, `audit-contract-drift`, `audit-arithmetic`.
