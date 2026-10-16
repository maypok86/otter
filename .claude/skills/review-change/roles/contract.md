# Role: contract

You check that what otter promises and what it does still agree after the change.

- **Public API.** For every exported identifier the diff adds, changes or whose behavior it
  changes: does the doc comment describe the new behavior, including the edge cases (absent
  and expired keys, zero and maximum durations and weights, errors, panics in user code,
  concurrent calls)? Is the change compatible: same signatures, same meaning for existing
  callers, the persisted snapshot format still readable? A silent change of meaning is
  `critical` if callers can rely on the old one.
- **Linearizability.** The maintainer prefers linearizable results. Does the change let a
  caller observe something no sequential order explains, against conditional writes
  (`SetIfAbsent`, `ComputeIfAbsent`) too?
- **Guarantees the diff writes down.** For every guarantee a comment or a document in the diff
  states (an order of events, "never", "only", "always"), try to break it with the cases the
  author may not have had in mind: another goroutine (maintenance, a refresh, the periodic
  clean-up), a caller-runs executor, a panic or `runtime.Goexit` in user code, an expired or
  in-place-updated node. A guarantee that holds only for some of them says so.
- **Stale references.** When the diff changes a function's role (what it reports, locks,
  hands over), grep `.claude/`, `docs/` and the doc comments for that function and report the
  places that still describe the old role.
- **Documentation.** `docs/` and `README.md` stay in step with the code. The user guide stays
  brief: edge-case rules belong in godoc, not in `docs/user-guide`.
- **Agent guidance.** For changes under `.claude/` (skills, docs, the auditor): every function,
  field, file, constant and behavior it cites exists and is described accurately; the
  conventions of the sibling files are kept (frontmatter, report paths of `audit-output.md`,
  the taxonomy); nothing in it references other projects (the repository's text explains otter
  on its own terms).

Cite the doc comment or document and the code that disagree.
