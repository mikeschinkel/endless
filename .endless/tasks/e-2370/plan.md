## Approach

**Classification (P1).** In `src/endless/session_cmd.py:_resolve_resume`, the branch reached when the task is set and its worktree is absent stops assuming a landing. A new helper, `_missing_worktree_kind(target, project_root)`, returns `landed` / `branch` / `never`, using `target["landed_sha"]`, `worktree_cmd._branch_exists(task_branch(id))` and the existing `_remote_copy_of_branch`. The no-intent refusal then picks its wording and the flag it names from that kind. Both surfaces get it, because `session goto --resume` already shares `_resolve_resume`.

**--recreate (P2–P5, P8).**
- `cli.py` session resume gains a boolean `--recreate`, exclusive with `--review`/`--reopen` (P4), next to the existing exclusivity check in `resume_session`. Set it as `intent="recreate"`.
- In `_resolve_resume`, intent `recreate` runs after every gate that already runs there (P5). The pane, rebind and transcript gates already run before `_resolve_resume` in `resume_session`, so nothing new is needed for them.
- It refuses unless the kind is `never` (P3). If a worktree exists, it refuses rather than resuming, so the flag cannot be mistaken for a no-op.
- `_recover_dropped_worktree` grows the `recreate` case rather than a parallel function. It reuses its epic refusal, and calls `worktree_cmd.recreate_dropped_worktree(task_id, root, base, detached=False)`. When no `task/N` exists, that already cuts a fresh `task/N` from `base`. `base` is the project's base branch, resolved the same way `task claim` resolves it (no hard-coded `main`).
- `decision_out` records `intent: recreate` and `status_to: null`. `--dry-run` (P8) must not create the worktree. That differs from the current `--review`/`--reopen` dry-run, which does create, so the recreate branch checks dry-run before calling `recreate_dropped_worktree`.

**Notice (P6).** In `resume_session`, when the decision's intent is `recreate`, argv becomes `["claude", "--resume", uuid, <notice>]`. The notice is a fixed string naming the worktree path, the base it was cut from, and the instruction to rebuild edits from the transcript.

**Bulk path (P7).**
- `internal/monitor/resume.go`: `fillRecoveryInfo` already fills `LandedSHA`.
- `internal/resumewindowscmd/resumewindows.go:decide` changes `review := f.Resolved && f.Worktree == ""` to also require a landing, carried in `facts` as a new `Landed bool` set from `target.LandedSHA != ""`.
- For a resolved task with no worktree and no landing, `decide` sets `Skip` with a message containing the `cd <root> && endless session resume E-N --rebind --recreate` line.
- The branch-exists-but-not-landed case is skipped too, naming `--reopen`, because Go cannot cheaply check remotes. Python's classification stays authoritative.

**Docs (P9).** Update the `session resume` docstring in `cli.py`, and the appendix-a and orchestration recovery text.

## Tests
- `tests/test_session_resume_recover.py`: one test per classification kind on bare resume; `--recreate` happy path (worktree created on a fresh `task/N`, status unchanged, notice in argv); each refusal (existing worktree, local branch, remote branch, landed); exclusivity; dry-run creates nothing; epic and declined still refused under `--recreate`.
- `internal/resumewindowscmd/resumewindows_test.go`: `TestDecide_DroppedWorktreeGetsReview` keeps passing with `Landed: true`; new tests for not-landed (skip, suggests `--recreate`).

**Task's own repo (P13).** `_recover_dropped_worktree` currently takes its root from `worktree_cmd._project_root()`, which resolves the project enclosing the shell's cwd. For the `recreate` intent and for `_missing_worktree_kind`, the root instead comes from the task's own `project_id` → `projects.path`. That keeps the worktree, the branch check and the landing check in the task's repo, even when resume runs from another project's directory. If the task's project has no registered path, the command refuses (REPORT) and creates nothing. `--review`/`--reopen` keep their current root resolution here (P11). Fixing them is E-2299, and fixing claim/spawn is E-2348.
