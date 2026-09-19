# E-1532 — Formalize task phases & statuses via FK to values tables — PLAN

Decisions locked 2026-06-23 (rev. 2026-06-24). **Blocked by E-1252** (status
renames): the seed must use E-1252's final names, so that decision lands first.

## Locked decisions
1. **One task.** Phases + statuses values tables, dropping `blocked`, the 9-row
   cleanup, and collapsing the duplicated validation — all here, not split.
2. **Blocked by E-1252.** Final status slugs come from the rename decision; the
   seed (and the one-time row rename) reflect its outcome.
3. **Drop `blocked` as a stored status** — not seeded. Blocking is derived from the
   `blocked_by` relation + blocker status (`endless guide tasks`); a stored value
   only duplicates it and goes stale (it bit E-1621) and carries no "why" (row 627
   proved the reason is unrecoverable without a relation). Removing it also
   dissolves the "what status on unblock?" problem — the real work-state is never
   overwritten.
4. **Enforcement = a real DB-level FK, by SLUG, done now (not deferred).** Keep
   `tasks.status`/`.phase` as TEXT and add `FOREIGN KEY(status) REFERENCES
   task_statuses(slug)` and `FOREIGN KEY(phase) REFERENCES task_phases(slug)`. The
   DB then rejects any invalid value on *every* write path (Go executor, raw SQL,
   Python), and the TEXT column means **no call-site churn** — every reader/writer
   keeps treating status/phase as the same string.
5. **Phase values** = the in-use five: `urgent, now, next, later, maybe`.
6. **Status values** = current set minus `blocked`, with E-1252 renames applied:
   (`needs_plan`→?, `in_progress`→?), `ready, verify, confirmed, assumed, completed,
   revisit, declined, obsolete`.

## Migration mechanics (the FK on existing columns)
SQLite can't ALTER-add a constraint, so the `.go` change-file (run with
`foreign_keys` OFF — the runner's default, per E-1571) does a standard `tasks`
table-rebuild:
1. create+seed `task_phases` / `task_statuses`;
2. run the 9-row cleanup (below) so no row holds a soon-invalid value;
3. `CREATE TABLE tasks_new (… same columns …, FOREIGN KEY(status) REFERENCES
   task_statuses(slug), FOREIGN KEY(phase) REFERENCES task_phases(slug), + existing
   self/other FKs)`;
4. `INSERT INTO tasks_new SELECT * FROM tasks` (ids preserved → 8 inbound-FK tables
   stay valid under FK-off);
5. `DROP TABLE tasks; ALTER TABLE tasks_new RENAME TO tasks`;
6. recreate the `tasks_updated_at` trigger and indexes (`idx_session_tasks_task`,
   `idx_project_next_tasks_task`);
7. `PRAGMA foreign_key_check` before the runner re-enables FKs.
`schema.sql` declares the post-state (the two tables + FK clauses on the `tasks`
CREATE) for fresh/sandbox DBs.

## Steps
1. **schema.sql** — add `task_phases(id, slug UNIQUE NOT NULL, label NOT NULL)` and
   `task_statuses(...)` with idempotent `INSERT OR IGNORE` seeds; add the two FK
   clauses to the `tasks` CREATE.
2. **`.go` change-file** — the rebuild above, against the populated real DB.
3. **Collapse validation to one source** — replace `cli.py:121 TASK_STATUSES` and
   the inline tuple in `task_cmd.py:3267` (and the phase choices) with a lookup of
   the seeded slugs; `click.Choice`/`MultiChoice` build from that single source.
   (The DB FK is the backstop; this keeps the friendly CLI error.)
4. **9-row cleanup** (re-verify the set at implementation time), inside the
   change-file before the rebuild:
   - 627 → `obsolete` (superseded by E-891).
   - 496, 520, 581, 616, 624, 634, 721, 1515 → `ready` if the row has non-empty
     text, else `needs_plan`; leave their `blocked_by` relations intact.
   One-time ledger hygiene (not shipped migration code).
5. **Apply E-1252 renames** — same change-file: `UPDATE tasks SET status=<new>` for
   each renamed slug, seed the table with new slugs, sweep code/docs/guide/handoff
   references off the old strings.
6. **Tests** — DB rejects an unseeded status (incl. `blocked`) and a bad phase;
   accepts each seeded one; cleanup leaves zero `blocked` rows; the rebuild
   preserved inbound FKs (`foreign_key_check` clean).

## Out of scope
- The rename strings themselves — decided by E-1252 (this task's blocker).



## Scope note (E-1723, 2026-07-05) — sole owner of the status-set definition
E-1648 was decoupled from this task (no longer blocked here); it ships the approval
gate on the current free-TEXT column and adds `submitted` as a plain value.

- **E-1532 solely owns** defining + FK-seeding the canonical status set and dropping
  stored `blocked` (Deliverable/step list above). E-1648 no longer claims these.
- **Seed must include `submitted`** — the approval-gate state E-1648 introduces
  (final name; supersedes E-1252's interim `unapproved`). Re-verify the full seed
  set (with E-1252's landed renames: `unplanned`/`underway`/`unverified`) at
  implementation time, and confirm `submitted` is present and `blocked` absent.
