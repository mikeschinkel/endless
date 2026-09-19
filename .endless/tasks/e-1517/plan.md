# E-1517 — Limit task titles to 100 characters

## Problem

Task titles have no length cap. Long titles wrap awkwardly in `task active` / `task list` (already truncated for display at ~80 chars), bloat `--llm` output, and signal that the author packed prose into the wrong field (long-form belongs in `--text`/plan). Memory note `feedback_task_title_shape.md` already codifies "≤100 chars (E-1517)" as a working norm; this task enforces it.

## Design

Add `TITLE_MAX_LENGTH = 100` and a length check at the front of `validate_title()` in `src/endless/task_cmd.py`. The check runs **before** the existing `force` bypass — length is a structural constraint, not the heuristic verb gate. Both `task add` and `task update --title` call `validate_title()`, so both write paths get the cap.

Scope decisions (agreed with Mike at file time):
- **Tasks only.** Decisions skip `validate_title()` today and stay exempt; the task brief says "task titles."
- **No `--force` bypass.** Length is structural. `force` continues to bypass the verb check only.
- **CLI only this slice.** The web UI's inline title editor (`internal/web/handlers.go:handleUpdateTaskTitle`) is a separate write boundary with no current cap — filed as a follow-up via `--cleans-up E-1517`, not fixed inline.

Existing rows: 16 tasks in the live DB exceed 100 chars (longest 165). Per no-migration-for-unshipped, leave them; updates that don't touch the title don't re-validate (validate_title only fires when `--title` is provided).

## Verification

- Unit: `tests/test_validate_title_length.py` covers accepts-empty / accepts-100 / rejects-101 / message format / `force` does NOT bypass.
- CLI integration: `task add` and `task update --title` reject 101-char titles with non-zero exit and a message naming both the actual length and the 100-char max.

## Out of scope (filed)

- Web UI inline title editor length cap (`handleUpdateTaskTitle`) — separate task with `--cleans-up E-1517`.
- Backfilling/shortening the 16 existing over-100 titles.
- Decision-title length cap.
