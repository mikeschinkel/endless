# E-1641 — Remove existing ghost session rows (light prune; FK-safe)

**Child of E-1640.** · **Layer:** SQL + small Python/Go command

## Correction from recon
Earlier worry was that an event-authoritative write path would resurrect deleted rows. **Recon
disproved this:** `internal/events/projector.go:73-111` replays **only task/decision events**;
session/note/message events are skipped (line 108). So `sessions` is **not** event-sourced and a
raw `DELETE` **is durable**. **Re-verify** this (read `projector.go`) as step 0 before deleting.

## FK-safety (the real constraint)
`sessions(id)` is referenced by **7 tables**: `session_messages`, `focuses`, `task_landings`,
`decisions`, `session_gates`, `project_next_events`, `project_next_revisions`. Before deleting,
confirm each candidate id has **zero** references across all 7; exclude any that are referenced.

## Candidate set
Strict (safe) criteria — **37 rows**:
```sql
WITH latest AS (
  SELECT active_task_id, MAX(started_at) AS max_started FROM sessions GROUP BY active_task_id
)
SELECT s.id FROM sessions s JOIN latest l ON l.active_task_id = s.active_task_id
WHERE s.state='ended' AND s.session_id IS NOT NULL AND s.kind_id=1
  AND (julianday(s.last_activity)-julianday(s.started_at))*86400 < 10
  AND (SELECT count(*) FROM session_statuses ss WHERE ss.session_id=s.id)=0
  AND s.started_at <> l.max_started;
```
The loose filter yields **~298** incl. NULL-`active_task_id` ghosts the "not newest per task"
guard can't cover. **Decide:** ship the strict 37 first; evaluate the NULL-task set separately
(guard e.g. "not the newest ended row per `process`/UUID"). Don't bulk-delete 298 blind.

## Approach — a repeatable command, not a one-off DELETE
Add `endless session prune [--dry-run]` (Python, alongside `session_cmd.py` verbs):
- `--dry-run` lists exactly which ids match + their FK-reference counts.
- Without it, FK-check each candidate, exclude referenced rows, `DELETE` the rest.
- Repeatable: useful until E-1640 stops new ghosts. Run **after** E-1640.

## Verification — create a per-task verify script
Create `tests/tasks/e-1641-verify.sh` in the `e-1624-verify.sh` shape (bash, `set -u`, the
`section`/`report_*`/`summary` helpers, `cd` repo root, ensure `go.work`, exit 0/1/2). Drive the
real `endless session prune` CLI against sandbox fixtures.

The script asserts (named checks):
- **Dry-run precision:** seed (fresh ids) N sub-10s ended ghosts + one ≥10s real session on a
  task; `endless session prune --dry-run` lists **exactly** the ghosts (with FK-ref counts) and
  excludes the real session.
- **FK-safety:** add a `session_messages`/`decisions` row referencing one ghost; assert the
  dry-run **excludes** that referenced ghost.
- **Prune effect:** after a real prune, the ghost-count query returns 0 for the seeded set; the
  real session and all 7 FK tables are intact.
- **Not event-sourced:** `endless-go event rebuild-db` (dry-run/temp DB) does **not** resurrect
  the pruned rows.

Re-runnability (sandbox NOT wiped between runs):
- Derive all seeded ids/`session_id`/`short_id` from freshly-allocated ids each run — never a
  fixed UNIQUE value (the prune is idempotent, but the *seeding* must not collide).
- Capture seeding output; `exit 2` loudly on any setup/seed error (never /dev/null).
- Run the script at least TWICE before declaring it done.

**Verify-handoff — tell the user to verify with (only) this:**
```
esu && ./tests/tasks/e-1641-verify.sh
```
