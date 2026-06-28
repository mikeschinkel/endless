# E-1655 — Collapse worktree+sandbox handling to canonical e-<id>

Implements ED-1515. Full rationale in E-1643's outcome (this task's research child).

> Anchors below are by file + symbol/function/regex name, NOT line numbers
> (line numbers drift as other branches land). Resolve current locations via
> LSP/grep before editing.

## Goal
Make one-worktree-per-task true by construction AND recognition: only
`.endless/worktrees/e-<id>` is recognized as a task's worktree. Drop the vestigial
`-<slug>` tolerance that half-supported named alternates (reaper-invisible; land/drop
silently picked the lexicographically-first match).

## Precondition (confirmed)
No `e-<id>-slug` worktree dirs exist in the wild. No migration needed.

## Changes
1. `WorktreePathForTask` (internal/monitor/worktree.go) — drop the `e-<id>-*`
   glob and the lexicographic-first pick; resolve only bare `e-<id>` (single Stat).
2. `_WORKTREE_TASK_ID_RE` (src/endless/worktree_cmd.py) — drop the optional
   `(?:-[a-z0-9-]+)?` suffix; remove any "first match of many" land/drop logic.
3. `TaskIDFromWorktreePath` regex (internal/monitor/worktree_lock.go) — drop the
   optional `-slug`.
4. Sandbox naming in src/endless/config.py — collapse to `e-<id>`; the
   `worktree_task_id` and `worktree_dir_name` helpers converge (keep one).
5. `reap_worktrees.go` — its regex is already anchored `e-(\d+)$`; remove the
   now-moot slug comment.
6. `db.go` — fix the `e-NNN[-slug]` comment to `e-NNN`.
7. Docs: CLAUDE.md (`~/.cache/.../e-NNN[-slug]/`, `endless-sandbox destroy
   e-NNN[-slug]`) -> `e-NNN`; `endless guide orchestration` — document the
   child-task model for multi-checkout needs (A/B, run-while-edit, bisect/snapshot
   -> child tasks).
8. Tests: update/remove any asserting slug-tolerance.

## Non-goals
No active block on raw `git worktree add` — a non-canonical dir simply stops being
recognized as the task's worktree; `create_task_worktree` already raises on a foreign
path collision.

## Verify (tests/tasks/e-1655-verify.sh, e-1624 shape)
- Go + Python suites green after the change.
- `WorktreePathForTask` resolves only bare `e-<id>`; a synthetic `e-<id>-x` dir is
  NOT resolved as the task's worktree.
- Sandbox dir derives from `e-<id>`.
