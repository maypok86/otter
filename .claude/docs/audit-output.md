# Audit output

Reports and transient analysis go under `.local/` (gitignored), grouped by producing model:

```
.local/audits/<model>/<skill-name>.md
```

- `<model>`: the short id of the model that orchestrated the run, lowercased, without the
  vendor prefix or context suffix (`opus-5`, `fable-5`, `sonnet-5`, `gpt-5.6`).
- `shared` takes the `<model>` slot for artifacts that aggregate several runs: the
  consolidated queue, a bug worked across sessions.
- `<skill-name>`: the invoking skill. A multi-agent run suffixes per agent
  (`audit-sibling-divergence-groupA.md`); a verification pass adds `-verification`. The
  unsuffixed name is the synthesis. **Never overwrite a report you were asked to verify,
  consolidate or read.**
- Witnesses (repro tests, probe programs) go under `.local/audits/<model>/<skill-name>-evidence/`,
  never into the source tree.

Rerunning an audit under the same model replaces its report. Keep `.local/` between
sessions; checked-in guidance must stay useful without it.

## Consolidated queue

When several audits have run, consolidate into:

- `.local/audits/shared/audit-consolidated.md`: counts by status and a table of item id,
  status, severity and one-line subject.
- `.local/audits/shared/queue/<item-id>.md`: one item per file, starting with
  `# <item-id>: <subject>`, `Status:` (`open`, `unverified`, `resolved`, `closed`) and
  `Severity:`. Then the claim, links to the source reports, the witness, counterarguments and
  the decision.

Reuse ids for recurring claims; never renumber. Keep resolved and closed decisions when a
claim comes back, with the contrary evidence.
