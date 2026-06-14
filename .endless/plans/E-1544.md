# E-1544 — Implement research creation gate with parent-epic exemption and `--justification` requirement

## Context

ED-1504 established that `--type research` is gated on either (a) parent being an in-progress epic, or (b) the user supplying `--justification` explaining why the research can't be inline. This task wires the gate into both `task add` and `task update`, plus the `--justification` flag and `tasks.notes` storage under a `## Justification` heading (until E-1531's typed `task_content` lands and migration is handled there).

E-1538 (task_types FK seeding incl. `epic`) is in flight (status=assumed); schema seeds at `internal/schema/schema.sql:123-127` confirm the four slugs already exist locally.

## Decisions (locked from chat)

- **Gate scope:** fires on both `task add --type research` and `task update --type research` (i.e. when the update payload sets `--type research`). Updates that don't touch `--type` do not re-evaluate the gate, even on a task that's already research and is being re-parented.
- **Redundant justification on exemption:** accept-and-store. The gate governs *requiring* justification, not forbidding it.
- **No-parent research:** gate fires (no exemption possible) — justification required.
- **Sticky-override statuses** (`revisit`, `blocked`, `declined`, `obsolete`) do **not** satisfy the exemption — only literal `in_progress`.
- **Notes collision on update:** if the target's `notes` already contains a `## Justification` heading, refuse with `click.ClickException` ("clear or edit notes manually first"). Idempotent re-write is NOT supported in v1.
- **Error message text** (one paragraph, no exemption hint per Mike's feedback): `--type research requires --justification explaining why the research can't be inline in a do-task.`
- **E-1531 migration:** out of scope. File a follow-up task with `--cleans-up E-1544` on E-1531 ("Migrate `## Justification` notes to typed `task_content` rows") before declaring this task verified.

## Implementation

### 1. Storage — Go side (event payload + executor)

Single field, atomic on create; allowed-field on update.

- `internal/events/payload.go:5-16` — add `Notes string \`json:"notes,omitempty"\`` to `TaskCreatedPayload`.
- `internal/events/executor.go:240-313` — `execTaskCreated`: include `notes` column in the `INSERT INTO tasks (...)` statement and pass `p.Notes` as the bound arg.
- `internal/events/executor.go:423-428` — `execTaskFieldsUpdated`: add `"notes": "notes"` to the `allowedFields` map so the `task.fields_updated` event can write notes.
- Update unit tests near `internal/events/executor_test.go` (or whichever file currently exercises `execTaskCreated` / `execTaskFieldsUpdated`) so the new field round-trips.

### 2. CLI flags (Python)

- `src/endless/cli.py:1153-1213` — `task add`: add `@click.option("--justification", default=None, help="Justification text for --type research (stored under '## Justification' in notes).")` and thread `justification` to `add_item(...)`.
- `src/endless/cli.py:1215-…` — `task update`: add the same `--justification` flag and thread it to the update function (`update_item` or similar — confirm the exact name during implementation).

### 3. Gate + storage logic (Python)

In `src/endless/task_cmd.py`:

- New helper `_research_gate_check(parent_id, justification)` (module-private):
  - If `justification` is non-empty → return (exemption-or-not, justification storage handled separately).
  - Else if `parent_id is None` → raise `click.ClickException` with the locked error text.
  - Else query parent type+status:
    ```python
    row = db.query(
        "SELECT t.status, COALESCE(tt.slug, '') AS type_slug "
        "FROM tasks t LEFT JOIN task_types tt ON tt.id = t.type_id "
        "WHERE t.id = ?", (parent_id,))
    ```
    If empty → `click.ClickException(f"Parent task E-{parent_id} not found")`.
    If `type_slug != "epic"` or `status != "in_progress"` → raise the gate error.
  - Uses the existing `db.query` pattern. The E-1486 Python→Go read migration is a separate epic; this follows the established convention for now.

- New helper `_compose_justification_notes(existing_notes, justification)`:
  - If `justification` is empty → return `None` (no notes change).
  - If existing notes contains `## Justification` heading (regex `r"(?m)^##\s+Justification\b"`) → raise `click.ClickException("notes already contains a '## Justification' section; clear or edit it manually before re-justifying.")`.
  - Else: section = `"## Justification\n\n" + justification.strip() + "\n"`. If existing notes is empty/None → return `section`. Else → return `existing_notes.rstrip() + "\n\n" + section`.

- `add_item` (`task_cmd.py:1477-1536`):
  - Add `justification: str | None = None` parameter.
  - When `task_type == "research"`: call `_research_gate_check(parent_id, justification)`.
  - If `justification` is non-empty: compute `notes = _compose_justification_notes(None, justification)` (always None on add — no existing task) and set `payload["notes"] = notes`.
  - Accept-and-store also fires when parent is epic+in_progress and `--justification` was passed (per Q2).

- Update path (the function `task update` dispatches to — confirm name during implementation):
  - Add `justification: str | None = None` parameter.
  - Detect "this update sets `--type research`" (i.e. caller passed `--type research`, regardless of whether the task is already research).
  - When true: determine the effective parent (the new `--parent` if the same update is changing it, else the current parent from the DB), then `_research_gate_check(effective_parent_id, justification)`.
  - If `justification` is non-empty: fetch existing notes via `db.query`, run `_compose_justification_notes`, include the result in the `task.fields_updated` `fields` dict as `"notes": <new_string>`.
  - When `--type` is NOT in the update (even for an existing research task), do not invoke the gate.

### 4. Guide update

- `docs/guide/tasks.md:75-88` (`## Adding tasks` block) — add 2–3 lines after the existing examples explaining the `--type research` gate: justification required unless `--parent` is an in-progress epic. One example line of each shape. Keep prose short; this is reference, not narrative.

### 5. Tests

Add a new file `tests/test_research_gate.py` (parallel structure to `tests/test_relations.py`). Use the same `isolated_env` fixture + `CliRunner` + real-DB pattern; do not mock `add_item`. Helper to seed an epic-in_progress task and a non-epic task once per test.

Cases:

**`task add`:**
1. research + epic-in_progress parent + no justification → exit 0, task created, notes empty.
2. research + epic-in_progress parent + justification → exit 0, notes contains `## Justification` + body.
3. research + epic-but-status=`ready` parent + no justification → exit ≠ 0, error text matches.
4. research + non-epic parent + no justification → exit ≠ 0, error text matches.
5. research + no parent + no justification → exit ≠ 0, error text matches.
6. research + no parent + justification → exit 0, notes contains `## Justification` + body.
7. non-research add (default `task`) + no justification → exit 0 (sanity — gate inert).

**`task update`:**
8. update `--type research` + parent is epic-in_progress + no justification → exit 0.
9. update `--type research` + non-epic parent + no justification → exit ≠ 0.
10. update `--type research` + justification on a task whose notes do NOT already have `## Justification` → exit 0, section appended.
11. update `--type research` + justification on a task whose notes ALREADY have `## Justification` → exit ≠ 0, refusal message.
12. update without `--type` on an already-research task whose parent is non-epic → exit 0 (gate doesn't fire when `--type` isn't in the update).

### 6. Follow-up filing (before flipping to verify)

Run from main checkout via `endless task add` — NOT from the worktree:

```
endless task add "Migrate '## Justification' notes to typed task_content rows" \
    --cleans-up E-1531 --type task --phase later \
    --description "When E-1531 ships typed task_content, migrate existing notes-stored \
    justifications written by E-1544 to a task_content row of type=justification."
```

(Adjust `--cleans-up` target if Mike prefers a different anchor; the spec says "filed under E-1531".)

## Critical files (touched)

- `internal/events/payload.go`
- `internal/events/executor.go` (+ corresponding `_test.go`)
- `src/endless/cli.py` (task add + task update flag wiring)
- `src/endless/task_cmd.py` (`add_item`, the update dispatcher, new helpers)
- `docs/guide/tasks.md`
- `tests/test_research_gate.py` (new)

## Verification

From the worktree `/Users/mikeschinkel/Projects/endless/.endless/worktrees/e-1544`:

1. `just build` — rebuild Go binaries (the event executor changed; stale binaries would silently drop `notes`).
2. `just test` — confirm new `tests/test_research_gate.py` plus existing suite all pass.
3. Manual smoke (uses the worktree's sandbox DB via `./bin-sandbox/endless`):
   - Create an epic: `./bin-sandbox/endless task add "Smoke epic" --type epic --status in_progress` → records id `E-X`.
   - Success without justification: `./bin-sandbox/endless task add "Smoke research" --type research --parent X` → succeeds.
   - Failure without justification: `./bin-sandbox/endless task add "Bad research" --type research` → fails with the gate message.
   - Success with justification: `./bin-sandbox/endless task add "Good research" --type research --justification "Cross-system comparison required."` → succeeds; verify `./bin-sandbox/endless task show <new_id>` exposes notes.
   - Update path: pick a sandbox task, run `./bin-sandbox/endless task update <id> --type research --justification "..."` → succeeds; rerun the same command → refuses (collision on existing `## Justification`).
4. `endless guide tasks` (or the worktree binary) — confirm the new research gate paragraph renders.
5. File the E-1531 follow-up task (§6). Do NOT mark it implemented here.
6. `endless task update E-1544 --status verify --db main`.
