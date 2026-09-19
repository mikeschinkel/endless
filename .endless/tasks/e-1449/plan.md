# Delete the plan-snapshot feature

Implements the decision to remove plan snapshots entirely.

## Scope

### Code deletions

**`cmd/endless-hook/claude.go`**
- Delete `snapshotPlanFile` (current lines ~892-967)
- Delete `existingSnapshot` helper (current lines ~969-1003)
- Delete the call site (current line ~422) plus the surrounding "Snapshot the plan content..." comment block (lines ~408-424). Keep the `isPlanFile` check at line 399 and the `SetPlanFilePath` call at line 404 — those are for ExitPlanMode plan-file tracking, unrelated to snapshots.

**`internal/events/commit.go`**
- Delete `CommitSnapshotPair` and any helpers that exist solely for it.
- Audit other functions in the file for snapshot-only references.

**`internal/events/commit_test.go`**
- Delete `TestCommitSnapshotPair_*` tests (4 known: FirstWriteCreatesCommit, SecondWriteAmends, LedgerHeadStartsNewCommit, StagedUnrelatedPathPreventsAmend, NonGitProjectFailsLoudly).

**`internal/monitor/db.go`**
- Delete `IsSandboxActive` (lines 40-52). Verify no other callers exist beyond `snapshotPlanFile` (already-confirmed by grep at planning time, but re-verify before delete).

**`internal/monitor/sandbox_test.go`**
- Delete the entire file.

**`src/endless/plan_cmd.py`**
- Delete `list_snapshots`, `show_snapshot`, `_snapshots_dir`, `_read_sidecar`, `_first_line`, `_project_path`.
- If the entire module becomes empty, delete the file (and any `from endless import plan_cmd` references).

**`src/endless/cli.py`**
- Delete the `snapshots_cmd` group (currently at lines ~1683-1706) and its two subcommands `snapshots_list`, `snapshots_show`.
- Note: the rename from `plan-snapshots` → `snapshots` already happened (commit c182cfa); only the `snapshots` group exists today.
- Do NOT touch the `session_statuses` snapshots at line ~682 — those are E-1312 session status records, unrelated to plan snapshots.

**`src/endless/worktree_cmd.py`**
- Drop `.endless/plans/snapshots/*` from `AUTO_COMMIT_GLOBS` (line ~56).

### Filesystem deletions

- `git rm -r .endless/plans/snapshots/` on the main checkout. 60+ files; commit as part of the deletion.
- Same `git rm -r` cleanup for any other worktrees that have local snapshots. (Worktrees that share the main `.git/` will see the deletion via the regular branch flow once it lands; per-worktree extra snapshots written before the deletion may need individual cleanup — verify before declaring done.)

### Documentation cleanup

- Search for documentation references to `plan-snapshots`, `snapshotPlanFile`, `CommitSnapshotPair` and remove.
- Likely candidates: `docs/`, `CLAUDE.md`, `README.md`, any `.endless/plans/E-*.md` that survived to main.

## Verification

1. `just build` succeeds with no references to deleted symbols.
2. `just test` passes (Python suite — removed snapshot tests, confirm nothing depended on them).
3. `go test ./...` passes.
4. From a Claude session in plan mode: `ExitPlanMode` works; plan-file path is still tracked for the task-attach flow (this exercises `isPlanFile` + `SetPlanFilePath` which stay).
5. `endless plan-snapshots --help` returns "no such command" (CLI surface gone).
6. `git status` on main checkout is clean (snapshots removed from tree).
7. PostToolUse hook on a plan-file Write produces no snapshot artifacts.

## Worktree handling

The originating worktree (`e-1362`) cannot be dropped while this Claude session is live in it — dropping would kill the session. A fresh worktree should be created for this implementation task per the one-task-per-worktree convention. The `e-1362` worktree can be reaped after this session ends or by Mike from outside.

## Out of scope

- `isPlanFile`, `SetPlanFilePath`, ExitPlanMode-related plan tracking — all stay, unrelated to snapshots.
- `monitor.ProjectPath` — has other callers.
- `.git/info/endless/verbs.json` work (E-1363) — independent.
- Worktree-land bundling of verbs.json (E-1364 rescoped) — independent.
