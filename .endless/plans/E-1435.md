# E-1435: Reformat task links output as flat table

## Problem

Today's output groups results under section headers with indented items. Hard to scan, especially for tasks with many relations:

```
$ endless task relations E-1421

Relations for E-1421
──────────────────────────────
Blocked by:
  E-1337 [in_progress] Replace worktree-delete-on-land with archive-and-reap pattern so Claude sessions stay resumable
  E-1416 [assumed] Guard against dirty unmanaged files in worktree as pre-condition for land
  E-1426 [assumed] Replace session companion JSON files with idempotent sessions.process registration on every hook
```

## Desired output

Match `task recent`'s flat-table layout, but replace the Phase and Tier columns with a Relation column:

```
$ endless task links E-1421

Relations for E-1421:
ID      Relation     Status       Title
──────  ───────────  ───────────  ──────────────────────────────────────────────────
E-1337  blocked_by   in_progress  Replace worktree-delete-on-land with archive-and-…
E-1416  blocked_by   assumed      Guard against dirty unmanaged files in worktree a…
E-1426  blocked_by   assumed      Replace session companion JSON files with idempot…
```

Column order: ID, Relation, Status, Title. Relation goes early because it's the distinguishing field for this view. Title still truncates at terminal width like `task recent` does.

## Scope and coordination

- **Target command**: `task links` (the surviving name per E-1230, which drops the `task relations` alias). If E-1230 hasn't landed yet, target whichever name is current AND ensure the change carries through E-1230's rename.
- **E-1231** (surface parent in this output): independent feature but lives in the same code path. Implementer may bundle if convenient, or leave to E-1231; coordinate to avoid double-edits.
- **E-1389** (rename `task_deps` table to `task_relations` + add `target_kind`): orthogonal storage change. This task is purely about presentation; should not depend on E-1389 landing.

## Behavior details

- Sort: by relation type, then by ID — keeps related rows grouped without using section headers.
- Empty: print `Relations for E-NNN: (none)` on its own line.
- `--llm` flag: keep the same content, just terse formatting (no decoration row).
- `--json` if supported: array of `{id, relation, status, title}` objects.

## Verification

1. `endless task links E-1421` — produces the desired table layout with header row + dashes + rows sorted by relation type.
2. `endless task links <task-with-no-relations>` — prints `Relations for E-NNN: (none)`.
3. `endless task links E-1421 --llm` — terse table, no ASCII separators, agent-friendly.
4. Coordinate with E-1230's rename: after E-1230 lands, `task links` is the canonical name; verify `task relations` and `task deps` (if still around as aliases per E-1230's decisions) behave consistently.

## Instructions for the implementing session

1. Confirm current state of E-1230 (`task relations` aliasing) before touching the command.
2. If E-1231 is unclaimed and bundling is straightforward, ask Mike before bundling.
3. Write the plan at `.endless/plans/E-1435.md` covering: code path identified (likely `task_cmd.py`'s relations-render function), exact column widths, sort order edge cases, and which existing flag conventions apply.
4. Attach via `endless task update E-1435 --text .endless/plans/E-1435.md` (auto-promotes to ready).
