Approach decision (with Mike, 2026-07-10): family (a) detect-and-present-
candidates, covering BOTH conflict sites (Step 3.7 orphan-replay + Step 4 main
rebase) via one shared message helper. Rejected alternatives:
- (b) leave-in-conflict (don't abort; user resolves + `git rebase --continue` +
  re-runs land): makes land stateful and re-entrant (verbs.json dedup on re-entry,
  user-self-abort races). Land is non-interactive by contract — rejected.
- (c) split the orphan-drop and user-commit replay into two rebases for structural
  attribution: extra rebase + its own rollback handling for marginal gain over
  reading the conflict state — rejected.
Key refinement from Mike: the recovery must be presented as CANDIDATES flagged as
possibly-incorrect, not one confident prescription. The current Step 4 message's
confident-but-wrong "auto-file" recovery is exactly the failure mode to avoid (it
misdirected the E-1750 land). Report facts confidently (step + files); offer
recoveries humbly.

Scope note: the original filing targeted only Step 3.7's "orphan auto-amend
cleanup failed" message and assumed Step 4's message was the good one to borrow.
The E-1750 land proved Step 4's message is ALSO wrong (confident auto-file
misattribution), so the fix covers both.

---

Added case (Claude session, 2026-07-09; folded into the plan's source-conflict
candidates): the branch commit RE-INTRODUCES content already landed for this task
— typically a `git commit --amend` of a commit that was already landed. Today
`just land` misattributes this to the E-972 auto-file rule and suggests
`git checkout main -- .endless/*.jsonl`, which cannot fix a source-file conflict.
For THIS cause the recovery is NOT "resolve + git rebase --continue" (that
duplicates the commit). It is: capture the new delta, reset the branch to base,
re-apply only the delta:

    git diff main..HEAD -- <source files> > delta.patch
    git reset --hard main && git apply delta.patch && git commit

Detection: if any conflicting path is a source (non-auto) file AND the branch is
not a descendant of the task's last task_landings SHA (see E-1308's proposed
gate), this cause is likely. Concrete repro: E-1750 (see E-1308).
