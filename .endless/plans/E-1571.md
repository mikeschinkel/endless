# E-1571 — Add sessions.active_epic_id column and kind discriminator for bg-agent rows

Lands after E-1565 / E-1566 (per-type templates, not directly dependent) and **before** E-1568 (--bg dispatch writes these columns) and E-1570 (attach verbs may read kind to discriminate fg vs bg).

## Decisions

### Schema additions
Two new columns on `sessions`:

- `active_epic_id INTEGER` — nullable FK to `tasks(id)`. Semantics:
  - **Coordinator (epic) fg row:** stable = the epic's task id; `active_task_id` mutates as user switches view to children.
  - **Bg-agent row:** `active_task_id` = the child task id; `active_epic_id` = parent epic id (resolved at spawn time by Python).
  - **Non-epic fg row:** NULL.
- `kind TEXT NOT NULL DEFAULT 'tmux'` — discriminator: `'tmux'` or `'background'`. App-level validation (no CHECK, per house rule). Backfill: all existing rows become `'tmux'`.

`process` column (the tmux pane id) is already nullable in the current schema. No structural change there — semantics shift: `kind='background'` rows legitimately leave it NULL. Document the invariant in a comment.

### Window-name / status-line rendering
Status-line `[E-NNN]` prefix in `internal/tmuxcmd/status_line.go::format()` (line 69) becomes:

- `active_epic_id IS NULL` → `[E-NNNN]` (today's behavior, unchanged)
- `active_epic_id IS NOT NULL AND active_epic_id = active_task_id` → `[E-EEEE]` (viewing the epic itself)
- `active_epic_id IS NOT NULL AND active_epic_id != active_task_id` → `[E-EEEE:E-CCCC]` (viewing a child of the epic)

Same logic applied to `internal/tmuxcmd/show_menu.go::format()` (line 75) for the popup header.

`monitor.ActiveTaskInfo` (the struct the format functions read) gains `ActiveEpicID *int64`. The query that populates it joins `sessions` (it already does — that's where `active_task_id` comes from) and selects `active_epic_id`.

### Coordinator row vs child-view: how active_task_id moves
Out of scope here — this child only adds the columns. The mechanism for "user switches focus to a child" is part of E-1570 (attach verbs) and the day-to-day `endless task claim` flow. This child does NOT implement focus-switching; it only ensures the data shape exists.

### "Focused bg agent" derivation
Per E-1552 outcome ("derived from the coordinator row, not stored"), no column added. Queries that need it compute it: `SELECT * FROM sessions WHERE kind='background' AND active_task_id = <coordinator's active_task_id>` — i.e. the bg agent whose child task matches what the coordinator is viewing. This child adds a Go helper `monitor.FocusedBgAgent(coordinatorSessionID) (*Session, error)` so callers don't reinvent the SQL.

### Validation
Go-side validator (`internal/monitor/sessions.go` or wherever existing validation lives — locate via grep at implementation): reject any insert/update where `kind NOT IN ('tmux','background')`. Mirrors the TaskType pattern (E-1538). No SQL CHECK.

Startup integrity check warns (not fails) if a `kind='background'` row has `process IS NOT NULL` or if a `kind='tmux'` row has `process IS NULL` while `state='working'` — the schema doesn't enforce these, but drift is interesting.

## Files

### New
- `internal/schema/changes/e-1571-sessions-active-epic-and-kind.sql` — change file for `just land`:
  ```sql
  ALTER TABLE sessions ADD COLUMN active_epic_id INTEGER REFERENCES tasks(id);
  ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT 'tmux';
  -- No backfill UPDATE needed: existing rows take the default 'tmux'.
  ```

### Changed
- `internal/schema/schema.sql` — declare the two new columns on the `CREATE TABLE sessions IF NOT EXISTS` block. Add the FK clause for `active_epic_id` analogous to existing `active_task_id` FK. Add a comment block above the new columns explaining the kind discriminator and active_epic_id semantics.
- `internal/monitor/sessions.go` (or whichever file holds session CRUD — locate at impl): expose `ActiveEpicID *int64` and `Kind string` on the Go session struct, include in SELECT/INSERT/UPDATE queries, validate `kind` on write.
- `internal/monitor/tmux.go` — extend `ActiveTaskInfo` with `ActiveEpicID *int64`, populate from query.
- `internal/tmuxcmd/status_line.go::format()` — branch on `info.ActiveEpicID`.
- `internal/tmuxcmd/show_menu.go::format()` — same branch.
- `internal/monitor/sessions_focus.go` (new file is fine, or fold into existing) — `FocusedBgAgent(coordinatorSessionID)` helper.

### Tests
- `internal/monitor/sessions_test.go` — round-trip a row with `kind='background'` + `active_epic_id` set; reject `kind='other'`.
- `internal/tmuxcmd/status_line_test.go` (or wherever format is tested) — three cases for the prefix render.
- `internal/monitor/sessions_focus_test.go` — `FocusedBgAgent` returns the right row given a coordinator + several children + matching/non-matching bg agents.

## Sequence note

Lands before **E-1568** (which writes `kind='background'` + `active_epic_id` rows) and **E-1570** (attach verbs may query `kind` to validate target).

## Verification

1. `just build` succeeds; `just land` applies the change file cleanly against a populated DB (test via sandbox: `endless-sandbox init e-test --mode populated`, then `endless db apply-change e-1571-sessions-active-epic-and-kind`).
2. `just test` passes (Go).
3. SQL probe: `SELECT name, type, "notnull", dflt_value FROM pragma_table_info('sessions') WHERE name IN ('active_epic_id','kind');` shows the new shape.
4. Manual: spawn a fresh fg session; confirm row has `kind='tmux'`, `active_epic_id IS NULL`. Status-line still shows `[E-NNNN]`.
5. Manual (synthetic): `UPDATE sessions SET active_epic_id = active_task_id WHERE id = <test row>; ` then check status-line shows `[E-EEEE]`. Then set `active_task_id` to a child id; status-line shows `[E-EEEE:E-CCCC]`.
