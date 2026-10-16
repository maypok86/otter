---
name: review-change
description: Review a diff or branch of otter with parallel reviewers specialized for this cache (blind concurrency, design invariants, regression patterns, contract, tests, performance, simplicity), then fact-check and triage every finding
argument-hint: "[branch or commit range; default: uncommitted changes, else the branch against main]"
context: fork
disable-model-invocation: true
allowed-tools: Read, Grep, Glob, Bash, Write, Agent, AskUserQuestion
---

# Review a change

You orchestrate the review: you gather the diff, pick the roles, dispatch them, have every
finding checked and triage the result. You do not review the diff yourself, and you give the
reviewers nothing of your own about it: no suspicions, no leads, no risk ranking. Every role
gets the same neutral material and reaches its conclusions alone.

## Step 1: the diff

`$ARGUMENTS` selects it:

- empty: the uncommitted changes, untracked files included (`git diff HEAD`, and
  `git diff --no-index /dev/null <file>` for each untracked file); if there are none, the
  current branch against `main` (`git diff main...HEAD`);
- a branch: `git diff main...<branch>`;
- a commit range: as given.

Write the diff, `git log --oneline` of the range and the list of changed files to a run
directory, `.local/reviews/<branch>-<short-sha>/`. When the head of the range is not the
checked-out tree (a branch or a merged range), add a detached worktree of it at
`<run dir>/tree` for the reviewers and checkers to read, and remove it when the report is
written. Also write `rules.md` there: the paths above, the base commit, the read-only rules
of Step 3 and the finding format, so that every prompt can point to it instead of repeating
it.

Empty diff: say so and stop. Over ~3000 lines: say that the review will be split by file
group, write one diff per group (production code with the docs it changes; tests), and run
Steps 3 to 5 per group, each with the roles its paths select. The `tests` role then runs
twice: on the code group for the `fix:` commits and their regression tests, and on the test
group for the tests the range adds or rewrites.

## Step 2: the roles

Each role is a file in `roles/` next to this skill. Pick the roles by the changed paths:

| Role | Runs when the diff changes |
|---|---|
| `blind-concurrency` | non-test Go code in the root package, `internal/` or `cmd/generator` |
| `design-invariants` | the same |
| `regression-patterns` | `cache_impl.go`, `mutation.go`, `policy.go`, `sketch.go`, `singleflight.go`, `task.go`, `persistence.go`, `internal/`, `cmd/generator` |
| `contract` | any non-test Go code (behavior the docs and `.claude/` describe), `docs/`, `README.md`, or `.claude/` |
| `tests` | any `_test.go` file, or any `fix:` commit in the range |
| `perf` | `cache_impl.go`, `mutation.go`, `policy.go`, `sketch.go`, `internal/`, `cmd/generator`, or a `perf:` commit |
| `simplicity` | any code or prompt |

A change only to `.claude/` or `docs/` gets `contract` and `simplicity`: there, a defect is a
claim about the code that the code does not support.

## Step 3: dispatch

Spawn one `general-purpose` agent per role, all in one batch. Never the `auditor`: it writes a
report of its own. Each prompt is:

1. the path of the run directory, and that the diff, the log and the file list are there;
2. the full text of the role file;
3. the finding format below;
4. the rules: read-only (no edits to the tree, no git state changes; scratch files only under
   the run directory); report only what the diff introduces or exposes, or a defect in a
   function it touches; apply the self-check; return the JSON array and nothing else.

`blind-concurrency` gets its own boundary, stated in its role file: it must not read
`AGENTS.md`, `CLAUDE.md`, `.claude/` or `docs/`. It cannot read `finding-taxonomy.md` either,
so put the severity levels and the categories of that file in its prompt, and leave
`design-decisions.md` out of its self-check.

### Finding format

```json
[{
  "role": "design-invariants",
  "location": "mutation.go:120-131",
  "title": "one line",
  "issue": "what is wrong, in full sentences",
  "evidence": "at most 5 quoted lines",
  "scenario": "the inputs or the interleaving, goroutine by goroutine, and what goes wrong",
  "severity": "critical | high | medium | low",
  "category": "a category of finding-taxonomy.md",
  "introduced_by_diff": true,
  "suggested_fix": "a concrete change"
}]
```

Severity and categories are those of `.claude/docs/finding-taxonomy.md`. Self-check before
emitting a finding: the cited lines say what the finding claims; the scenario is reachable
through the public API with legal inputs; no guard on the path prevents it; it is not
documented as intentional in `design-decisions.md` (except for `blind-concurrency`, which does
not read it); the consequence is observable. A finding that fails any of these is dropped,
not softened.

## Step 4: merge and check

1. Parse each array; a malformed one gets one retry, then is dropped and listed.
2. Merge findings about the same defect at the same place, keep the most specific location,
   the highest severity that the scenario supports, and the list of roles that raised it.
3. Drop a `blind-concurrency` finding that `design-invariants` or `design-decisions.md`
   explains as intentional, and count it.
4. Number the rest `R-1`, `R-2`, ...
5. Check every finding with its own `general-purpose` agent, all in one batch. The checker
   treats the reviewer as fallible: it reads the cited code with its surroundings, traces the
   scenario, looks for a guard and for an entry in `design-decisions.md`. It runs what can be
   run: a test for a `high` or `critical` finding or any claim of a runtime defect, the
   suggested change for a claim that code is redundant (remove it and run the tests), the
   wider configuration for a claim that a test is restricted. It works in a scratch copy or a
   temporary worktree under the run directory, removed afterwards, runs with `-race`, and
   compares head and base when the finding says the diff introduced it; a fake clock or a
   synchronous executor only steers the schedule, and the consequence is confirmed with the
   default executor and the real clock. It returns `confirmed`, `rejected` or `unresolved`,
   its own severity, whether the diff introduced the defect, the evidence (commands and their
   output) and, for `unresolved`, the question that would settle it.
6. Ask the user the questions of the `unresolved` findings in one `AskUserQuestion`, or mark
   them `escalated` if a stress test, not an answer, would settle them.

## Step 5: triage and report

Label each confirmed finding with the triage labels of `finding-taxonomy.md`: `patch` (fix in
this change), `defer` (pre-existing: a separate branch from `main` and a `fix:` commit),
`escalated`. Rejected findings are `reject`. Where the reviewer and the checker disagree on
severity or on whether the diff introduced the defect, take the checker's, which ran the
code. For a range that is already merged, say that every `patch` is now a follow-up on
`main`.

Write the report to the run directory as `report.md` and return it:

- a header: the range, the commits, the roles that ran, the counts per label;
- `patch`, then `escalated`, then `defer`, each ordered by severity and then by the number of
  roles that raised it: the location, the issue, the scenario, the checker's evidence in a
  sentence, the roles, the fix;
- rejected findings in one line each, and the ones where roles disagreed (blind raised it,
  design-aware explained it) with the reason;
- the number of blind findings filtered as intentional.

When nothing survives, say how many findings were raised and why each was rejected, rather
than calling the change clean.
