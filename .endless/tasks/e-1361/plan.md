# E-1361 — Storage of verbs.json and plan snapshots

## Location

`<main-checkout>/.git/info/endless/` — machine-local, untracked by the project repo, shared across all worktrees of the project.

## Files

- `verbs.json` — single mutable file; overwritten on each `endless verb add`.
- `snapshots/pending/<ts>-<sha>.{md,json}` — immutable. Companion file holds the `session_id`. Used when a snapshot is taken before any task is claimed by the session.
- `snapshots/e-NNN/<ts>-<sha>.{md,json}` — immutable. At task claim, pending entries matching the claiming session's `session_id` transition to this directory.

## Persistence

Plain files. No side git repo. Snapshots are immutable (content-hashed filename); verbs.json is rewritten in place.

## Reach to project main

Via `endless worktree land`:

- `verbs.json` is **copied** into the worktree's tracked tree. Dedup-against-main converges concurrent additions across worktrees.
- `snapshots/e-NNN/` is **moved** into the worktree's tracked tree. Per-task scope, immutable per file, so no need to retain the side copy after a successful land.

Both are committed on the worktree branch as part of land, then merged to project main.

## Sandboxed sessions

Share `.git/info/endless/` with non-sandboxed sessions of the same project. DB sandboxing (E-1281's `XDG_CONFIG_HOME` routing) is independent and continues to apply to the SQLite DB only.

A generic override mechanism for sessions that need a separate `.git/info/endless/` (testing verb or snapshot behavior) is the scope of a separate task.
