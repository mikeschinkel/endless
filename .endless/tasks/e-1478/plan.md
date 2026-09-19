# Surface task landings in the read CLI

## Goal

Make the git-landing data that `endless worktree land` already records visible
in the task read surface. Every land emits a `task.landed` event; the Go
projector writes one row per land into the `task_landings` table
(`task_id`, `session_id`, `branch`, `merge_commit_sha`, `landed_at`). Landing
is append-only (a task can land more than once) and does NOT change task status.

## Decisions

1. **`Landed:` line in `task show`** — most recent `landed_at` (formatted by
   `_format_timestamp`, like the existing `Confirmed:` line) + the latest
   land's short merge SHA, with a `(landed N times)` suffix only when more than
   one landing row exists. Shown only when at least one landing row exists.
   Example: `Landed:     2026-06-14 9:31 pm  160272f  (landed 5 times)`
   Added to all three outputs: default human, `--llm`, and `--json`.

2. **`endless task landed` command (arg-driven)**:
   - Bare `task landed` → lists tasks that have at least one landing, ordered by
     most-recent `landed_at` descending (peer to `task active` / `task recent`).
   - `task landed <id>` → that task's full landing history: every row
     (`landed_at`, `branch`, `merge_commit_sha`), newest first.

3. **Read path — Python now, migrate to Go later (decided with Mike
   2026-06-17).** The original plan gated this work behind E-894 (move
   task-display reads to Python→Go) so the landing reads would land directly on
   the Go path. Mike unblocked it: seeing what has and hasn't landed without
   writing SQL is a live pain point, and the data is already recorded (75
   landings at decision time). The reads ship now on the existing Python
   `db.query` path. When E-894 / E-1482 (`query_bridge.py`) move the
   task-display reads to Go, the landing reads (`_task_landings`, `landed_list`,
   `landed_item`) belong in that same migration.

## Implementation (2026-06-17, this task)

In `src/endless/task_cmd.py`:
- `_task_landings(item_id)` — all landing rows for a task, newest first.
- `_format_landed_line(landings)` — renders `TS  shortsha  (landed N times)`,
  count suffix only when >1, tolerates a missing SHA.
- `detail_item()` fetches landings once and emits the `Landed:` line / `landed=`
  field / `"landed"` JSON object across the three render paths.
- `landed_list()` + `_render_landed_table()` — bare-list command body
  (ID / Landed / Lands / Title), grouped by project, with `--llm` / `--json`.
- `landed_item()` — per-task landing history, with `--llm` / `--json`.

In `src/endless/cli.py`:
- `task landed` command with an optional `item_id` argument plus
  `--project / --all / --limit / --llm / --json`.

Tests: `tests/test_task_landed.py` (14 cases) — `_format_landed_line` units,
the `Landed:` line across human/llm/json, never-landed omission, and
`task landed` list + history including ordering and the empty case.

## Verification

- `endless task show <landed-id>` shows the `Landed:` line with timestamp +
  short SHA; a 5-landing task renders `(landed 5 times)`; a single-landing task
  has no suffix; a never-landed task shows no `Landed:` line.
- `endless task landed` lists landed tasks newest-first;
  `endless task landed <id>` shows full history newest-first; never-landed shows
  "Never landed".
- `--llm` and `--json` carry the landing fields in both commands.
- `uv run pytest tests/test_task_landed.py` → 14 passed.
