# E-1272 — Replace worktree land's destructive recovery hint; drop auto-commit step

## Context

Per E-987's resolution, after E-1206 / E-1208 / E-1275 all commit endless-managed files at write time, worktree land's batch auto-commit step at `worktree_cmd.py:702-730` has nothing left to do — any dirt in those files at land time indicates a routing/write-time bug (or stale-state edge case), not normal accumulation. The destructive recovery hint at `:744-759` ("git checkout main -- ...") is dangerous if a real conflict ever occurs (it silently overwrites legitimate changes). Both should change.

The just-filed E-1309 (route ledger commits to main regardless of caller cwd) plus the already-landed write-time commits make this cleanup safe: by the time someone tries to land, there shouldn't be uncommitted endless-managed dirt on main; if there is, it's an invariant violation worth surfacing, not silently bundling.

## Changes

### 1. Drop the batch auto-commit step (worktree_cmd.py:702-730)

Replace the `_git_status_partition` + auto-commit logic with a single refusal check: if `git status --porcelain` from main shows ANY dirty paths — auto-managed OR otherwise — refuse with a message that names the dirty paths, points at the write-time-commit invariant, and instructs the user to investigate.

`AUTO_COMMIT_GLOBS`, `_is_auto_commit_path`, and `_git_status_partition` themselves stay in place — they're still used by tests and may be useful for diagnostic logging. Only the *behavior* changes: no more bundling-and-committing.

### 2. Replace the destructive recovery hint (worktree_cmd.py:744-759)

Replace the `git checkout main -- <globs>` recovery hint with a non-destructive investigation message: name the conflicted paths from the rebase error, point at the E-972 routing invariant, and instruct the user to investigate (`git log -p`, `git blame`) rather than overwrite. Mention E-1309 as the underlying fix in flight.

## Critical files

- `src/endless/worktree_cmd.py:702-730` — drop auto-commit step
- `src/endless/worktree_cmd.py:744-759` — replace recovery hint

## Verification

1. `just test` — existing suite passes. `tests/test_worktree_land_dedup.py`'s `AUTO_COMMIT_GLOBS` / `_is_auto_commit_path` tests should still pass since the constants/helpers remain.
2. Smoke: in a worktree with no dirt on main, `endless worktree land E-NNN` succeeds (unchanged).
3. Smoke: synthesize uncommitted `.endless/verbs.jsonl` dirt on main, attempt land — should refuse with the new message naming the dirty path.

## Out of scope

- Removing `AUTO_COMMIT_GLOBS` constant / dependents — defer.
- Refactoring `_dedup_worktree_verbs_against_main` — separate concern.
- E-1309's actual implementation — separate task.
