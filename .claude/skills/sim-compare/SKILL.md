---
name: sim-compare
description: Compare otter's hit rate across git revisions (and against other caches) on the simulator traces, with repeated runs and a significance mark
argument-hint: "[refs, e.g. main,WORKTREE] [traces or configs] [reps]"
context: fork
disable-model-invocation: true
allowed-tools: Read, Grep, Glob, Bash, Write
---

Measure whether a change to the eviction policy, the climber, the sketch or the write path
changes otter's hit rate, and explain any difference.

Adapted from Caffeine's `sim-compare` and `sim-analyze`.

## Input

$ARGUMENTS

Defaults: refs `main,WORKTREE` (`WORKTREE` = the current tree with uncommitted changes),
all configs in `benchmarks/simulator/configs/` that parse, 5 repetitions, cache `otter`.

## Why repetitions

otter randomizes its hashing per cache, so one run on a small trace varies by up to ±3 points
(more at capacities where the climber has several plausible resting places). The expected
effect of most policy changes is well under a point. A single run decides nothing:

- at least 5 repetitions per cell for a decision, 3 for a first look;
- the revisions are interleaved run by run, so load on the machine hits both alike;
- a cell is marked `*` when the difference exceeds twice its standard error and 0.1 points.
  With many cells some marks are chance: trust a pattern across capacities or traces, or
  re-run the marked cells with more repetitions.

## Step 1: run

```bash
python3 .claude/skills/sim-compare/scripts/compare.py \
    --refs main,WORKTREE --reps 5 --caches otter \
    benchmarks/simulator/configs/oltp.toml benchmarks/simulator/configs/p8.toml
```

The script checks each ref out as a detached worktree under `.local/sim/trees/` (gitignored),
runs `go mod tidy` and builds the simulator there, runs every config `--reps` times and writes
`results.csv` and `summary.md` to `.local/sim/runs/<timestamp>/`. The simulator renders a
chart through headless Chrome after printing its results and may hang there; the script stops
each run once it reports completion, so do not run the simulator by hand without a timeout.

Notes:

- Cost: small traces (gli, oltp, loop) take seconds per run; p8, s3 and ds1 take minutes and
  gigabytes. Start small; run the large ones only to confirm.
- `configs/zipf.toml` generates a new random trace every run, which adds trace variance on
  top of hashing variance.
- `configs/scarab.toml` defines `paths` twice and does not parse; the script skips it.
- To compare against other caches, pass `--caches otter,lru,theine,s3-fifo,arc,clock-pro` (names from
  `benchmarks/simulator/internal/simulator/simulator.go`, `getPolicies`).
- To test an experiment without committing it, keep it in the working tree and compare
  `HEAD,WORKTREE`. To compare two experiments, commit each on a scratch branch.
- Remove stale worktrees with `git worktree remove .local/sim/trees/<name>` (or
  `git worktree prune` after deleting the directory).

## Step 2: read the result

For each marked cell, and for any consistent shift across capacities:

1. Is it reproducible? Re-run that config with `--reps 10`.
2. Is it plausible from the change? Read the diff between the refs. A change to the write path
   that moves the hit rate means the policy now sees different signals (accesses, misses,
   reconcile tasks); find which.
3. What is the trace like? When a result surprises, characterize the trace with a short
   scratch script (under `.local/sim/`) over the decoded keys:
   - distinct keys versus capacity (is the cache tiny relative to the working set?);
   - frequency skew (share of requests to the top 1% of keys);
   - recency (share of re-accesses within the last C requests);
   - scans (runs of keys never seen before);
   - phase shifts (overlap of the hot sets in the first and second half).
   Frequency-skewed traces reward the main region, recency and shifting traces reward the
   window; that is where the climber matters.
4. Compare with `lru` (and `arc` or `s3-fifo`) at the same capacities: a regression that drops
   otter below LRU is worth more than one that keeps it well above.

## Step 3: report

Write `.local/sim/runs/<timestamp>/report.md` and return a summary:

- the refs (with commit hashes), traces, capacities, repetitions;
- the table of significant and borderline cells, with mean ± sd for each ref;
- for each real difference: the trace, the capacities, the size, and the explanation found
  in Step 2;
- what was not run (large traces, other caches) and why.

Do not tune constants to win a trace. A change that helps one trace and hurts another needs
the maintainer's decision, with both numbers.
