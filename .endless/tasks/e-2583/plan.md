# Compute whether a pass stands instead of storing unlanded

From the E-2577 brainstorm. A status records something an actor did. "A pass
stands at the current code" is a comparison, and storing it needs a sweep to
keep it true; that sweep demoted E-2486 behind the user's back after a failed
land, and both `land` and `verify` then refused.

## Changes

- Remove `unlanded` from the transition table. The land moves `unverified`
  to `assumed`, or to `landed` when post-land items are open.
- A pass stays a record: the commit it tested and the main commit verify
  rebased onto (E-2263 adds the rebase).
- One Go function answers whether the latest pass stands: main has not moved
  past the recorded main commit, and the branch's own work is unchanged by the
  measure in the sibling task (Endless's own commits, such as mirrors and CTRF
  reports, never count).
- `task show`, `task next`, `session status` and the land call it. Nothing
  else stores it.
- The land refuses when no pass stands and names `verify` as the next command.
  When main has moved, it says so. It does not run the suite itself.
- Delete the sweep's `unlanded → unverified` demotion and its staleness diff.
- Migrate tasks now at `unlanded` to `unverified`; their passes keep standing
  or not by the new function.
- Regenerate the lifecycle diagram (`just lifecycle-index`).

## Verify

A suite proving: a pass survives a rebase onto an unmoved main and an Endless
auto-commit; a pass stops standing when main moves or the branch's own lines
change; the land refuses with the verify instruction in both cases; a failed
land no longer changes the task's status; no `unlanded` status remains.
