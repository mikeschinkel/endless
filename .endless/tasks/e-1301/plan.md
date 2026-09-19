# E-1301 — Audit and migrate readers of `.endless/worktree.json` task_id field

## Context

E-1298 surfaced the "man with two watches" problem: a stale `.endless/worktree.json` left in main from an older session reported `E-1186` while tmux's DB-backed lookup correctly showed `E-1298`. The recipe trusted the companion file and tried to land the wrong task. The fix in E-1298 stopped reading the companion locally, but other code paths still trust the field. This task closes the systemic loop.

Per the task description, the design intent (matches Mike's framing):

> The companion file can stay as a marker of 'endless-managed worktree' for foreign-tree classification, but its `task_id` must not be a source of truth.

The right alternative source of truth is the **path convention** (`<project>/.endless/worktrees/e-NNN[/-slug]?`). The directory name IS the canonical task ID, established by `endless task claim` and enforced by the convention. Path-extraction is:
- Cheap (string regex; no file read, no DB query)
- Stale-proof (the dir name doesn't drift; if the path is renamed by hand, the worktree is broken at a more fundamental level)
- Self-documenting (the path matches what the user sees in `cd` and `ls`)

`internal/monitor/companion.go:WorktreePathForTask` already uses path convention going the other direction (task → path); E-1301 makes the reverse direction (path → task) consistent.

## Inventory of readers

Eight call-sites, six Python + two Go. All but two are lookups; the other two are display.

| # | File:line | Function | Use | Migration |
|---|---|---|---|---|
| 1 | `src/endless/worktree_cmd.py:238` | `_enriched_list` | Display task in `worktree list` | path-extraction |
| 2 | `src/endless/worktree_cmd.py:276–277` | `current_worktree` | Display task in `worktree current` | path-extraction |
| 3 | `src/endless/worktree_cmd.py:336` | `for_task` | Find worktree row by task_id | path-extraction (or use `WorktreePathForTask` semantics) |
| 4 | `src/endless/worktree_cmd.py:516–520` | `_branch_for_task` | Find worktree row by task_id (used in `land`) | path-extraction |
| 5 | `src/endless/worktree_cmd.py:624` | `create_task_worktree` | Reject double-claim by checking existing companion | path-extraction |
| 6 | `src/endless/task_cmd.py:_worktree_for_task` | Plan-file routing ownership | path-extraction |
| 7 | `cmd/endless-hook/claude.go:1102` | `resolveCwdTaskID` | PostToolUse task auto-binding | path-extraction |
| 8 | `cmd/endless-hook/claude.go:1372–1383` | Task-mismatch tool-use block message | path-extraction |

## Design

**Step 1**: introduce two small helpers and use them everywhere.

- **Python**: `_task_id_from_worktree_path(p: Path) -> str | None` in `src/endless/worktree_cmd.py`. Regex-extracts `E-NNN` from a path matching `.../\.endless/worktrees/e-(\d+)(-[a-z0-9-]*)?(/|$)`. Returns the canonical `E-NNN` or `None` if no match. Pure function, no I/O.
- **Go**: `TaskIDFromWorktreePath(path string) string` in `internal/monitor/worktree_lock.go` (sits next to `FindWorktreeRoot`). Same semantics, returns `""` on no match.

**Step 2**: replace each `comp.task_id` / `comp.TaskID` read with the helper.

For the lookups (#3, #4, #5, #6, #7), the typical shape changes from:

```python
# before
target = next(
    (r for r in rows
     if r["companion"] and r["companion"].get("task_id") == canonical),
    None,
)
# after
target = next(
    (r for r in rows
     if _task_id_from_worktree_path(Path(r["path"])) == canonical),
    None,
)
```

For display readers (#1, #2), substitute the path-derived value. Display matches the existing column expectations because path-extraction returns the same `E-NNN` string when the convention holds.

For Go #7 (`resolveCwdTaskID`):

```go
// before
comp, err := monitor.ReadWorktreeCompanion(wtRoot)
if err != nil || comp == nil || comp.TaskID == "" {
    return 0
}
taskID, err := parseEndlessTaskID(comp.TaskID)
// after
tid := monitor.TaskIDFromWorktreePath(wtRoot)
if tid == "" {
    return 0
}
taskID, err := parseEndlessTaskID(tid)
```

`FindWorktreeRoot` (which walks up looking for `.endless/worktree.json`) still uses the companion **as a marker of "endless-managed"**, but no longer reads its task_id field. Consistent with Mike's "marker, not source of truth."

**Step 3**: stop writing `task_id` to the companion file on new worktree creation (`worktree_cmd.create_task_worktree:650`). The struct field stays (with `omitempty`) for backward-compat reads — old companion files keep working — but the staleness surface shrinks: new worktrees never carry a writable copy that could lie.

**Step 4**: when reading any code path that still encounters a populated `task_id` field (legacy companion files), compare it to the path-extracted task_id. On disagreement, emit a stderr warning so the staleness is visible. The path-extracted value wins; the warning is informational, not blocking. This catches legacy-companion drift without re-introducing trust in the field.

## Scope clarification: cwd-based vs worktree-path-based readers

Two classes of readers:

**Worktree-path-based (6 of 8 sites)** — input is an already-resolved worktree path; cwd is irrelevant.
- `_enriched_list` iterates `git worktree list` and reads each row's path.
- `for_task`, `_branch_for_task`, `_worktree_for_task`, `create_task_worktree` start from a task ID and check the deterministic path `.endless/worktrees/e-NNN/`.
- Go's task-mismatch block (claude.go:1372) operates on an already-resolved `worktreePath`.

For these, path-extraction is correct regardless of session cwd.

**cwd-based (2 of 8 sites)** — input is cwd; walks up.
- `current_worktree`: from a worktree, extracts E-NNN; from main, no extraction (correctly reports "main, no task").
- `resolveCwdTaskID` (Go hook): from a worktree, returns E-NNN; from main, returns 0 — no auto-bind attempted. This is correct for the "user starts in main, claim creates a worktree, session stays in main" case: the explicit `task claim` already wrote the DB session→task binding, so the hook does not need to also auto-bind. The DB binding (`sessions.active_task_id`) remains the canonical source for "what task is this session on" regardless of cwd; `endless-tmux active-id` reads it correctly from anywhere.

The plan does not regress the main-cwd-but-task-bound scenario. Path-extraction answers "what task is THIS PATH for"; the DB binding answers "what task is THIS SESSION on". Different questions, different sources.

## What this does NOT touch

- `internal/monitor/companion.go:WorktreePathForTask` — already path-based; no change.
- The DB session→task binding (`sessions.active_task_id`) — that's a separate canonical source for "which task is this session working on" and remains untouched.
- The companion's `branch`, `base_branch`, `created_at`, `kind` fields — out of scope; this task audits only `task_id`.
- E-1303 (release-prompt on land/terminal) — overlapping in spirit (stale-state cleanup) but different mechanism. Stays independent.

## Code surface

**Files to modify:**
- `src/endless/worktree_cmd.py` — add `_task_id_from_worktree_path` helper; update 5 call-sites (#1–#5).
- `src/endless/task_cmd.py` — update `_worktree_for_task` (#6); import the helper.
- `internal/monitor/worktree_lock.go` — add `TaskIDFromWorktreePath` function.
- `cmd/endless-hook/claude.go` — update `resolveCwdTaskID` (#7) and the task-mismatch block (#8).

**Tests to add or update:**
- `tests/test_worktree_create.py` (or similar) — unit tests for `_task_id_from_worktree_path` (matches, no-match, with-slug, in-subdir, in-main-checkout).
- `internal/monitor/worktree_lock_test.go` — unit tests for `TaskIDFromWorktreePath`.
- Existing tests for `_branch_for_task`, `for_task`, `current_worktree`, etc. — verify behavior unchanged when worktrees follow the convention.
- New regression test: a worktree with a deliberately-wrong companion `task_id` (e.g., simulating the E-1186 stale case) — verify readers ignore the companion's lie and use the path.

## Verification

1. **Stale-companion immunity (regression)**: create a worktree at `.endless/worktrees/e-999/`, hand-edit its companion's `task_id` to `E-12345`, run `endless worktree current` from inside the worktree. Expect: `Task: E-999` (path wins; companion's lie ignored).
2. **Main checkout reports no task**: with no `.endless/worktree.json` in main (clean state) OR with a leftover stale one, `endless worktree current` from main reports `Task:` empty. The lie-prone case is fundamentally fixed by path-extraction.
3. **All Go tests pass**: `go test ./...` green.
4. **All Python tests pass**: `just test` green.
5. **Smoke**: `endless worktree list` shows correct tasks for live worktrees; `endless worktree for-task E-NNNN` resolves the right path.

## Out of scope

- Removing the `task_id` field from the companion file. Keeping it for human readability is harmless once nothing reads it as source of truth.
- A migration script to clean up existing stale companion files. The fix is read-side; stale files become irrelevant after migration.
- E-1303 / E-1304 (release prompt, recipe defensive check). Tracked separately.

## Resolved during planning

- **Helper location (Python)**: in `worktree_cmd.py`, imported from `task_cmd.py`. Adds a forward `task_cmd → worktree_cmd` import; no cycle.
- **Disagreement warning**: emit a stderr warning when the legacy companion's `task_id` disagrees with path-extraction. Path-extraction wins; warning is informational, helps spot stale companion files.
- **Stop writing `task_id` to new companion files**: shrink the staleness surface. Old companions keep their field (we don't migrate disk state); new writes omit it. Struct field stays with `omitempty` so reads of legacy files still parse.
