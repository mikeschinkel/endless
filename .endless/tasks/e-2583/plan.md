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
  against its merge-base with main, context excluded, and Endless-committed
  files excluded. `git patch-id` cannot do this, so it is a small routine.
- "Endless-committed files" means exactly the files Endless itself writes and
  commits (ledger, verbs, mirrors, lessons, CTRF reports), named by one Go
  list. `monitor.AutoManagedStatusGlobs` holds only the ledger and verbs today,
  and the land's Step 3 auto-commit has its own Python notion of
  endless-managed files: reconcile those into the one list (checking the
  anomaly callers before widening it), do not add a third. It is NOT all of
  `.endless/`:
  a task's own verify suite and plan are the branch's work, and an edit to the
  suite after a pass must void it (`TestASuiteEditStalesThePass` stays).
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
  past the recorded main commit in any file outside the Endless-committed list, and the
  measure in section 1 is unchanged. Commits on main that touch only
  Endless-committed files (another task's CTRF report, ledger, mirrors, lessons) are
  Endless's bookkeeping and never void a pass (ED-1657). The pass compares
  content, never commit ids, so a land's rebase that rewrote the branch's
  SHAs but left its lines alone leaves the pass standing (absorbs E-2582).
- `task show`, `task next`, `session status` and the land call it. Nothing
  else stores it.
- The land refuses when no pass stands and names `verify` as the next command.
  When main has moved, it says so. It does not run the suite itself.
- The land's uncommitted-changes refusal (Step 2) names the one Endless file
  that can be sitting on main uncommitted through no fault of the user: another
  task's verify report (`.endless/tasks/e-*/verify-*.ctrf.json`), written onto
  main by `task verify` moments before that run commits it. When every listed
  file is such a report, the refusal says whose it is and that retrying after
  the run commits is the whole remedy. The land never commits or waits on it
  (absorbs E-2582, superseded by this task).
- Delete the sweep's `unlanded → unverified` demotion and its staleness diff.
- Migrate tasks now at `unlanded` to `unverified`; their passes keep standing
  or not by the new function.
- Regenerate the lifecycle diagram (`just lifecycle-index`).

## Verify

A suite proving: a clean rebase with context drift leaves the measure equal; a
change to an added line changes it; a commit touching only Endless-committed files
leaves it equal; an edit to the task's verify suite changes it; the gate accepts a reconciliation across such a rebase; a pass survives a
rebase onto an unmoved main, an Endless auto-commit, and a main commit that
touches only Endless-committed files; a pass survives a refused land that rewrote the
branch's commit ids; a pass stops standing
when main moves or the branch's own lines change; the land refuses with the
verify instruction in both cases; a land refused for another task's uncommitted
verify report on main names the report and says to retry; a failed land no longer changes the task's
status; no `unlanded` status remains.


