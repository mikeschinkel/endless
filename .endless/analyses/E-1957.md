## Note from E-2122 (2026-09-08) — the function this task edits has changed shape

E-2122 ("Report what git actually said when worktree land's rebase fails")
rewrote the surroundings of `_rebase_conflict_message`. Nothing here is
invalidated, but Deliverable 1 now lands on a smaller target than when this plan
was written.

### What changed

Both land call sites (Step 3.7 orphan replay, Step 4 main rebase) previously
caught `CalledProcessError` and called `_rebase_conflict_message`
unconditionally, so every non-zero `git rebase` exit was reported as a content
conflict. They now go through a dispatcher, `_rebase_failure_message`, which
picks one of three reports from evidence rather than from the exit code:

- a rebase that was ALREADY in progress before land started — reported as that,
  left running (land no longer aborts an operation it did not begin)
- unmerged paths present — a real conflict; delegates to
  `_rebase_conflict_message`, unchanged in substance
- anything else — "rebase failed", git's stderr verbatim, and NO recovery
  candidates

Git's stderr is now threaded from the exception into all three; it used to be
captured and discarded.

`_rebase_conflict_message` also had one internal change: the REBASE_HEAD read is
gated on a rebase actually being in progress. REBASE_HEAD is a plain ref, so a
rebase that never started could otherwise report a commit from an unrelated
operation as "your commit that failed to replay".

### What it means for Deliverable 1

Deliverable 1 ("delete the two numbered candidates, end with one instruction:
run `endless worktree diagnose`") still applies, and now applies ONLY to the
conflict branch. The other two branches already offer no candidates — the
non-conflict report deliberately offers none, on the grounds that there is
nothing to judge between once git has named the cause.

So the hazard this task exists to remove — a user following a numbered recovery
that silently reintroduces deleted identifiers — is now reachable through
exactly one path instead of every non-zero rebase exit. The candidates
themselves are untouched and still need deleting.

The auto-file-only confident path is likewise untouched, and this task's
instruction to keep it still holds.

### Cross-reference

E-1347's analysis says the "(none reported)" misclassification "belongs to
E-1957 (and E-1272 for the destructive hint)". The misclassification half is
discharged by E-2122. The destructive-recovery half is still this task's.

E-2122 is pending land at the time of this note; if it has not landed, the
description above is of `task/2122-report-what-git-actually-said-when`.
