# E-1694: Split spawner (↩ from) from real parent (↑ parent) in session status

## Problem

`endless session status` labels the **spawning session's active task** as `↑ parent`.
That is session lineage, not the task tree. The focal task's real
`tasks.parent_id` is never shown. Origin: E-1465's plan defined `↑ parent` as
"the parent session's active task" — a naming mistake (spawner ≠ parent) that
landed in E-1465 (confirmed).

Concrete case: window for E-1462 (real parent E-1461) was spawned by a session
whose active task is E-1684 (a *sibling* of E-1462). `session status` shows
`↑ parent E-1684`; `task show` correctly shows `Parent: E-1461`.

## Decision (settled with Mike)

- **Table view**: keep the spawner row but relabel it `↩ from`; **add** a new
  `↑ parent` row driven by the focal's real `tasks.parent_id`. Both always show
  (like the focal), regardless of terminal status.
- **`--tree` view**: root the ancestry spine on the **real parent**; annotate the
  focal node inline as `*E-<focal> ← E-<spawner>` (this ← spawner) when the
  window was spawned. The spawner is no longer the tree root.
- Legend grows by one entry (`↩ from`) — Mike accepted the extra width.

## Surfaces

### 1. `internal/monitor/session_status.go`
- `SessionStatusRow`: add `IsFrom bool`. **`IsParent` now means the real
  task-tree parent** (changed meaning), `IsFrom` is the spawner (the old
  `IsParent` behavior).
- `SessionStatusRows` query:
  - Add CTE `rpar(rpid) AS (SELECT parent_id FROM tasks WHERE id = focal)`.
  - Keep `pfoc(ptid)` = spawner's active task (from `parentSession`).
  - base UNION: add the real-parent task (`tasks t, rpar WHERE t.id = rpar.rpid`).
  - enr: `is_from` = (b.id == pfoc.ptid && != focal) [old is_parent];
    `is_parent` = (b.id == rpar.rpid && != focal).
  - Final WHERE: `... OR is_parent OR is_from OR status NOT IN (terminal)`.
  - If spawner == real parent (rare), both flags true; classify precedence
    (parent > from) resolves the single rendered row.

### 2. `internal/sessionstatuscmd/session_status.go`
- Add `actFrom` to the action enum, between `actParent` and `actDoing`;
  icon `↩`.
- `legend`: `● this  ↑ parent  ↩ from  ⟳ doing  ▶ do  ✎ plan  ◷ orphan  ☑ verify | ⊗ blocked  ⏸ blocks`.
- `classify`: focal > parent > from > doing > status switch. (`IsParent`→
  actParent, `IsFrom`→actFrom.)
- Sort ranks follow the enum order automatically.

### 3. `internal/sessionstatuscmd/tree.go`
- `parentID(rows)` returns the `IsParent` (real parent) id; add `fromID(rows)`
  returning the `IsFrom` spawner id.
- `buildSpine(focal, parent, from, backlogRoots)`: root on `parent` (real); the
  focal `treeNode` carries the spawner id for inline annotation.
- `treeNode`: add `from int64`; `nodeLabel` for the focal appends ` ← E-<from>`
  when `from > 0`.
- `renderTree` passes both ids; `doPlanIDs` unaffected (parent & from rows
  classify as actParent/actFrom, already excluded).

### 4. Tests
- `internal/monitor/session_status_test.go`: assert `IsFrom` on the spawner row,
  `IsParent` on a seeded real-parent row; the old "parent" assertions move to
  `IsFrom`.
- `internal/sessionstatuscmd/session_status_test.go`: classify table gains
  from-row case; legend assertion updates; the `↑ E E-1461` prefix case becomes
  the real-parent row.
- `internal/sessionstatuscmd/tree_test.go`: `renderSpineString` signature gains
  `from`; add a case asserting `*E-<focal> ← E-<spawner>` and parent-rooted spine.

## Verify
`tests/tasks/e-1694-verify.sh` (sandbox DB, headless `--task`): seed a focal with
a real parent and a spawner session; assert the table shows `↑ parent <real>` and
`↩ from <spawner>` as distinct rows, and `--tree` roots on the real parent with
the focal annotated `← <spawner>`. Plus `just test`.
