# Plan: make `rebuild-db --confirm` refuse deliberately

`rebuild-db --confirm` cannot run today and MUST NOT be made to run. It aborts
on any project with a session bound to a task, and that abort is the only thing
standing between the command and a large amount of unrecoverable data. This task
replaces the accident with an intention. It does NOT repair the rebuild — that
belongs to **E-799**, the event-sourcing epic, which is where "SQLite as a
rebuildable projection" is actually owned.

## What the command does today, measured

`runRebuildDB` (`internal/eventcmd/event.go`) replays the ledger into a scratch
database, then copies back exactly three tables: `tasks`, `decisions`,
`decision_relations`. The copy is spelled as `DELETE FROM <table>` followed by
`INSERT ... SELECT * FROM proj.<table>`.

The full transitive closure of that `DELETE FROM tasks`, derived from
`pragma_foreign_key_list` over a fresh schema, not by reading:

| Reached                     | Action   | Restored? |
|-----------------------------|----------|-----------|
| `task_landings`             | CASCADE  | **no — destroyed** |
| `session_gates` (`epic_id`) | CASCADE  | **no — destroyed** |
| → `report_judgments`        | CASCADE  | **no — destroyed (second hop)** |
| → `report_labels`           | CASCADE  | **no — destroyed (second hop)** |
| `sessions.task_id`          | SET NULL | no |
| `sessions.epic_id`          | SET NULL | no |
| `session_statuses.task_id`  | SET NULL | no |
| `decisions.origin_task_id`  | SET NULL | yes |
| `tasks.parent_id`           | SET NULL | yes |

Two of those are SECOND-HOP: `session_gates` cascades onward. Anything that
walks only the direct children of `tasks` will miss them.

`task_deps` is a fourth gap of a different kind: the projector builds it, the
copy-back skips it, and no FK reaches it — so a rebuild silently leaves every
blocking relation at whatever the live database happened to hold.

And per **E-1041**, the projection's INPUT is already known-bad: replay logs FK
and UNIQUE failures, and committed test fixtures reuse real task ids 1–150 in
~36 of 47 ledger segments. So the output cannot be trusted even where the
copy-back is correct.

## The one thing this task builds

Refuse in `runRebuildDB`, BEFORE the projection is built and before any
transaction opens, whenever `--confirm` is passed. Print what the command would
destroy — counted from the live database, not described in the abstract:

    rebuild-db --confirm is disabled: it would destroy data the ledger cannot
    restore.

      N task_landings rows      (landing history)
      N session_gates rows      (and N report_judgments, N report_labels)
      N sessions bindings       (violates ED-1560)
      task_deps                 not rebuilt at all — left stale

    The projection is also unreliable while E-1041 is open.
    Repairing this is E-799. Do not remove the write-once trigger or the
    `ON DELETE SET NULL` on sessions.task_id to make this command run — see
    "Sequencing" in E-2062.

The dry run (no `--confirm`) keeps working exactly as it does now: it is
read-only, it builds the projection, it prints counts. That is the useful half
of the command and this task does not touch it.

## Sequencing — the trap this task exists to disarm

`sessions.task_id ON DELETE SET NULL` is a STANDING VIOLATION of ED-1560. The
invariant says the binding is set once and never cleared; the FK says it is
cleared whenever the task row goes. It has never fired only because tasks are
almost never deleted and because the write-once trigger aborts it when they are.
`session_tasks` already documents the correct shape — no FK at all, so "rows
must outlive their referenced session/task."

So the FK action is wrong and should eventually go. **But removing it also
removes the abort**, and the abort is what currently prevents the four CASCADE
rows above from being destroyed in silence. Fixing the schema first converts a
loud refusal into quiet data loss.

The order is therefore fixed:

1. This task's explicit guard.
2. E-799 makes the rebuild whole (copy back what the projector builds; suppress
   the notice triggers; decide what `task_deps` and the session tables mean).
3. Only then drop `sessions.task_id`'s FK action, and delete this guard.

Write step 3 down here because step 3 looks obviously correct in isolation.

## Traps for whoever does the repair under E-799

Recorded now, while they are measured, so the repair does not rediscover them:

- **An upsert fires `tasks_notify_sessions`.** It is `AFTER UPDATE ON tasks`, so
  today's delete-then-insert never triggers it, but replacing that pair with
  `ON CONFLICT(id) DO UPDATE` would notify every live session about every task
  whose projected value differs. The notice triggers must be suppressed for the
  duration of any copy-back, the way
  `internal/schema/changes/e-1969-rename-sessions-task-id.go` drops and
  re-creates a trigger inside its transaction.
- **Copying `task_landings` back fires `task_landings_notify_sessions`**
  (`AFTER INSERT`), announcing every historical landing to every live session.
  The E-2005 schema comment currently calls that replay "harmless" and says so
  ON THE GROUNDS that `event rebuild` copies back only three tables — correct
  today, wrong the moment the repair lands. Correct that comment in the same
  commit.
- **A projected `task_landings.session_id` may name a session this machine has
  never had.** The ledger is shareable; `sessions` is machine-local. NULL it on
  copy rather than failing the FK.
- **`PRAGMA foreign_keys=OFF` is not the way out.** SQLite ignores it inside a
  transaction, so it would have to fence the whole replacement — and then a
  genuinely-lost task id is deleted with enforcement off, leaving dangling rows.
  It trades a loud abort for silent corruption, which is the same trade as
  dropping the trigger. (`monitor.DB()` sets `SetMaxOpenConns(1)`, so the usual
  pooled-connection objection does NOT apply here.)

## Not in scope

- `session_statuses` is a dead feature being removed under separate work. Count
  it in the refusal if it still exists when this is built; do not design around
  it and do not remove it here.
- `decisions` / `decision_relations` are self-consistent: the only dependent
  cascades and is replaced in the same transaction. Leave them alone.

## Verification

- `rebuild-db --confirm` refuses on a database with a bound session, naming the
  counts, and writes NOTHING — assert the row counts of `task_landings`,
  `session_gates`, `report_judgments`, `report_labels` and `sessions.task_id`
  are identical before and after.
- It refuses on a database with NO bound session too. The guard is about what
  the command would destroy, not about whether the trigger happens to catch it —
  a project with landings but no bindings is the case today's accidental fuse
  misses entirely.
- The dry run (no `--confirm`) still builds the projection and prints its counts,
  unchanged.
- The write-once trigger and every `REFERENCES tasks(id)` clause are still
  declared in `schema.sql`: assert it, so a later sweep cannot "fix" the abort by
  removing the fuse.
