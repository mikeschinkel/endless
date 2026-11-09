# Compute whether a pass stands, from one measure of the branch's own work

From the E-2577 brainstorm (ED-1657). One cause: Endless measured "the code
changed" in several inconsistent ways, and each fired on events that changed
nothing it protects. The reconciliation fingerprint (`git patch-id`) hashes
context lines, so a rebase onto a main with nearby edits staled identical
verdicts (E-2486's R3 to R5). The sweep's staleness diff compared whole trees,
so anything main gained read as a change to the branch. And `unlanded` was a
stored comparison, so a sweep had to keep it true, and it demoted E-2486 after
a failed land.

## 1. One measure of the branch's own work

- One Go function hashes the branch's added and removed lines per file,
  against its merge-base with main, context excluded, `.endless/` excluded.
  `git patch-id` cannot do this, so it is a small routine.
- The reconciliation gate uses it in place of patch-id.
- Existing reconciliations store a patch-id. Recompute the new measure for each
  at its recorded commit, or treat a stored patch-id as current once on first
  read; pick whichever the stored commit makes reliable.
- E-2479's re-reconcile at `unverified` stays for real changes to the
  branch's lines.

## 2. Compute whether a pass stands

- Remove `unlanded` from the transition table. The land moves `unverified`
  to `assumed`, or to `landed` when post-land items are open.
- A pass stays a record: the commit it tested and the main commit verify
  rebased onto (E-2263 adds the rebase).
- One Go function answers whether the latest pass stands: main has not moved
  past the recorded main commit in any file outside `.endless/`, and the
  measure in section 1 is unchanged. Commits on main that touch only
  `.endless/` (another task's CTRF report, ledger, mirrors, lessons) are
  Endless's bookkeeping and never void a pass (ED-1657). The pass compares
  content, never commit ids, so a land's rebase that rewrote the branch's
  SHAs but left its lines alone leaves the pass standing (absorbs E-2582).
- `task show`, `task next`, `session status` and the land call it. Nothing
  else stores it.
- The land refuses when no pass stands and names `verify` as the next command.
  When main has moved, it says so. It does not run the suite itself.
- Delete the sweep's `unlanded → unverified` demotion and its staleness diff.
- Migrate tasks now at `unlanded` to `unverified`; their passes keep standing
  or not by the new function.
- Regenerate the lifecycle diagram (`just lifecycle-index`).

## Verify

A suite proving: a clean rebase with context drift leaves the measure equal; a
change to an added line changes it; a commit touching only `.endless/` leaves it
equal; the gate accepts a reconciliation across such a rebase; a pass survives a
rebase onto an unmoved main, an Endless auto-commit, and a main commit that
touches only `.endless/`; a pass survives a refused land that rewrote the
branch's commit ids; a pass stops standing
when main moves or the branch's own lines change; the land refuses with the
verify instruction in both cases; a failed land no longer changes the task's
status; no `unlanded` status remains.
