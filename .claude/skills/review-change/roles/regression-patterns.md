# Role: regression patterns

You check whether the change resembles a bug otter has had. Read the list "Historical bug
patterns" in `.claude/agents/auditor.md`, then `git log --oneline main -- <changed files>` for
the files the diff touches, and read the `fix:` commits among them that concern the same
functions. `gh issue list --repo maypok86/otter --search <term>` finds the reports.

For every pattern the diff comes near:

1. Name the pattern and the commit or issue it comes from.
2. Show how the change could bring it back: the path the earlier fix closed, and whether the
   change reopens it, moves it or adds a path around it.
3. Check that the regression test of the earlier fix still covers the changed code. A test
   that no longer reaches the path is a finding too.

Patterns that come up often in this code, in addition to the list: a write and a refresh or
load of the same key racing over who wins; a task built or handed over in a different place
than before (ordering of `OnDeletion`, inline maintenance on the writer); a deadline read
before the lock and used after it; a value updated in place under a reader that does not lock.

Report only resemblances you can make concrete. A file in common is not a finding.
