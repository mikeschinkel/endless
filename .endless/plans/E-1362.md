# Move `snapshotPlanFile` target to `.git/info/endless/snapshots/{pending,e-NNN}/`

## Context — E-1361's decision

E-1361 (confirmed) decided: verbs.json and plan snapshots are stored as plain files in `<main-checkout>/.git/info/endless/`, shared across the project's worktrees, and reach project main only via worktree land. Snapshots are immutable per-task entries; verbs.json is a single mutable file.

For snapshots specifically: pending entries (no task bound to the session yet) live in `<main>/.git/info/endless/snapshots/pending/`. When a task is claimed against the session, pending entries belonging to that session move into `<main>/.git/info/endless/snapshots/e-NNN/`. At worktree land, E-1364 moves the `e-NNN/` contents into the worktree branch's `.endless/plans/snapshots/` so they reach project main.

This task implements that decision for the snapshot side. E-1363 implements it for verbs.json. E-1364 ties them into worktree land.

## What changes

### 1. New write target

`snapshotPlanFile` in `cmd/endless-hook/claude.go:898` today writes to `<project_root>/.endless/plans/snapshots/<ts>-<sha8>.{md,json}`, where `<project_root>` comes from `monitor.ProjectPath(projectID)` (DB lookup).

New target:
- `<main-checkout>/.git/info/endless/snapshots/pending/<ts>-<sha8>.{md,json}` when no task is bound to the session.
- `<main-checkout>/.git/info/endless/snapshots/e-NNN/<ts>-<sha8>.{md,json}` when the session has an active_task_id.

Existing filename convention (`<ts>-<sha8>`) preserved.

### 2. Resolve main checkout via git, not `ProjectPath`

**Critical:** do NOT resolve the new target via `monitor.ProjectPath()`. That call goes through the DB; under the pre-E-1429 implicit-DB-choice world it can return the worktree checkout (when the hook's `XDG_CONFIG_HOME` points at a sandbox whose `projects` table has the worktree as the registered project, via `ensureAutoRegisteredProject`'s auto-insert behavior) instead of the main checkout. E-1361's decision says snapshots are *always* in main checkout's `.git/info/`, regardless of DB context — so use git itself to find the main checkout, not endless's project registry.

Use git directly:
```
git -C <cwd> rev-parse --git-common-dir
```
returns the main `.git/` directory regardless of which worktree the call is made from. The snapshot target is `<git-common-dir>/info/endless/snapshots/`.

This decouples the snapshot path entirely from the DB-choice question — E-1429 doesn't affect this task and this task doesn't depend on E-1429.

### 3. Sweep at task claim

When a session claims a task (`endless task claim E-NNN` or `endless task bind`):
- Scan `<main>/.git/info/endless/snapshots/pending/` for entries owned by this session.
- Move them to `<main>/.git/info/endless/snapshots/e-NNN/`.
- The "owned by this session" check: each snapshot's companion-style metadata (filename or in-file) needs a session_id. Decide during planning: filename embeds session UUID, or each `.json` file carries it, or a sidecar index. E-1361's outcome says "session_id in companion file" for pending entries — confirm what that means concretely in the planning step.

Open question: is the sweep idempotent (safe to run more than once if claim flow re-fires)? Should be yes.

### 4. Remove `IsSandboxActive()` gating

`snapshotPlanFile` has an `IsSandboxActive()` check (added by E-1354) that skips snapshot commits in sandboxed sessions. Once snapshots no longer commit at write time (they're written, not committed; E-1364 commits them via worktree land), the sandbox-skip is dead code. Remove the call from `snapshotPlanFile`.

The `IsSandboxActive()` helper itself can stay (may be useful elsewhere) or be removed if no other caller exists. Decide during planning based on cross-reference search.

### 5. Migration of existing snapshots

Today's snapshots already exist under `<project>/.endless/plans/snapshots/` across multiple worktrees and the main checkout. Decide during planning:
- Leave them in place; new snapshots go to the new target only. Old ones never reach main via the new land mechanism but were presumably already committed via the old AUTO_COMMIT_GLOBS path.
- Or migrate (move/copy them into `.git/info/endless/snapshots/` retroactively).

My lean: leave in place. Old snapshots are already in their respective worktrees; the new mechanism starts fresh. Confirm with Mike.

## Coordination

- **E-1429 (require explicit `--db`)** — E-1429's hook-uses-main rule would also make `ProjectPath` resolve to main, but we don't rely on that; we use `git rev-parse --git-common-dir` instead. The two tasks don't conflict and either can land first. E-1429's prompt has a corresponding note.
- **E-1363 (move verbs.json)** — sibling implementation child of E-1361. Independent; can land in either order.
- **E-1364 (worktree land bundles snapshots)** — blocked-by this task. Once snapshots live in `.git/info/endless/snapshots/e-NNN/`, E-1364 moves them into the worktree branch at land time so they reach project main.

## Open design items — present to Mike, do not decide alone

1. **Session-id ownership of pending entries.** How is "this pending snapshot belongs to this session" encoded? Filename suffix (`<ts>-<sha8>-<session-uuid>.json`), in-file `session_id` field, or sidecar index? E-1361's outcome says "companion file" — clarify the concrete representation.
2. **Migration of existing `.endless/plans/snapshots/`.** Leave in place vs. retroactively migrate. My lean: leave in place.
3. **Removal of `IsSandboxActive()` helper.** Remove entirely vs. keep for future callers. Decide after cross-reference search.

## Verification

1. From a Claude session: edit and exit a plan via `ExitPlanMode`. Snapshot lands at `<main-checkout>/.git/info/endless/snapshots/pending/<ts>-<sha8>.{md,json}`.
2. Same session then claims a task (`endless task claim E-NNN`). The pending snapshot is moved to `<main-checkout>/.git/info/endless/snapshots/e-NNN/`.
3. From a Claude session in a sandboxed worktree: snapshot also lands at `<main-checkout>/.git/info/endless/snapshots/...` — NOT at `<worktree>/.git/info/...` (confirm `git rev-parse --git-common-dir` correctly returns the shared `.git/`).
4. `IsSandboxActive()` call removed from `snapshotPlanFile`; tests still pass.
5. Subsequent `endless worktree land E-NNN` (E-1364) moves the snapshots from `.git/info/endless/snapshots/e-NNN/` to the worktree's `.endless/plans/snapshots/` and bundles them into project main (verified once E-1364 lands).

## DO NOT decide unilaterally

The open design items above must be presented to Mike with options and trade-offs at planning time. Do not pick on your own.
