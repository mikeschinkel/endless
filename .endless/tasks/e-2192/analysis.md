`worktree land` applies only NEW files under `internal/schema/changes/` (`_branch_schema_changes` in `src/endless/worktree_cmd.py`). Versioned goose migrations under `internal/schema/migrations/` are never applied at land time. They reach the main database only when an INSTALLED binary next connects (`monitor.DB` applies on connect; a worktree candidate opens main schema-passive, E-1818).

But Step 6 of the land emits `task.landed` with the worktree's own candidate endless-go, compiled from the landing branch. If that branch's code touches its new schema during the emit, the emit fails against the unmigrated database, and main is left advanced with the landing unrecorded.

Observed on E-2188: migration 00009 added `sessions.focus_task_id`; the `task.landed` emit ran `upsertSessionTask`, which now sets focus, and failed with `no such column: focus_task_id`. Recovery was waiting for an incidental migration by the installed binary, then re-running `just land`. E-1813's 00008 got through only because its emit touched nothing new.

Fix direction: at Step 5.5 (after the ff-merge, before `task.landed`) apply the landing branch's migrations with the migration-only executable (`bin/endless-migrate`, ED-1571), never with a candidate endless-go (ED-1567), behind the same pre-apply backup the `changes/` path takes. The record-only re-run must apply them too.

Related to E-2020: once connect stops applying schema, a candidate emitting against a behind database is REFUSED rather than failing on a missing column, so this gap does not go away there.
