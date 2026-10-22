# Read across projects over ATTACH

E-2215 child 4. E-2257's inventory lists the machine-side reads that need
project-only tables, so a mirror can't serve them:

- auto-spawn and prime eligibility (`eligibleQuery`, `primeEligibleQuery`):
  open blockers, plan present, no open questions;
- session status (`SessionStatusRows`, `SessionStatusRowsForSession`) and
  `SessionGraphData`: blockers, supersedes/duplicates, plan presence, landed;
- project monitor (`ProjectStatusRows`): landed;
- rater (`unratedSubmittedTasks`, `raterContext`): plan and context bodies;
- question routing (`taskQuestions`, `resolveQuestionTarget`);
- worktree verify state (`VerifyStates`): `verified_sha`;
- project list/status, `task next` across projects, the web dashboard;
- Python `_hint_same_session_root_cause`.

## Work

- One helper that ATTACHes every registered (non-ignored) project file up to
  SQLite's limit of 10. E-2218 adds the opt-out above that.
- Rewrite each read as a `UNION ALL` over the attached files' views, or as a
  machine query for the task set followed by per-project queries.
- Schema-qualify every project table in these reads (see the
  unqualified-name note in the per-project database task).
