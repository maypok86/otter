# Trap generators

Copied unchanged from Caffeine (https://github.com/ben-manes/caffeine, Apache License 2.0) at
commit 92cd6cca5fed23d8343ef7caf338631c977ae9ec: the trace generators of
`.claude/skills/climber-gate/` and `workload.py` with the specs of `.claude/skills/audit-regret/`
that its gate table runs. Each one writes a deterministic LIRS trace (one key per line).
`parity.py traces` runs them with the arguments of that gate table.

When the port follows a newer Caffeine commit, refresh these files and the `TRAPS` table in
`parity.py` from the same commit.
