# E-1415 — Validate resolved session id against the DB

## Context

The session-id resolver in `src/endless/task_cmd.py` returns whatever
`endless_session_id` is recorded in a matching `.endless/sessions/claude-*.json`
companion file without checking that the id actually exists in the `sessions`
table.

During E-1307 verification we observed: a Claude session spawned inside a
worktree sandbox writes `endless_session_id=1` (the sandbox DB's first row) into
the **main checkout's** companion file. After `endless worktree land` reaps the
sandbox, the companion file persists in the main checkout. The id `1` now points
at an unrelated old `sessions` row (or nothing) in the real DB. Every consumer —
`endless session id`, `endless session show`, `endless task bind`,
`endless task update`, the E-1401 attribution gate in `event_bridge.emit_event`
— trusts the resolver and silently misattributes work to the wrong session.

The upstream cause (sandbox→main companion bleed during land) is tracked by
E-1332 / E-1337 / E-1391. This task is the **centralized validation gate**: when
the upstream bug recurs (or a new one writes a similarly bad companion), the
resolver detects it, refuses to resolve, and names the offending file loudly.

## Approach

Validate every companion-derived candidate id against the `sessions` table.
On validation failure, fail loudly with a structured diagnostic — never fall
through silently to another layer.

Per Mike's answer to plan questions:
- **Sibling-layer policy:** "reject any stale group" — if *any* sibling
  companion is stale, the whole sibling resolution fails (no filter-then-recount).
- **Caller signal:** "structured return" — add a `SessionResolution` value type
  so callers (gate message, `endless session id`) can produce stale-companion-
  aware error text without parsing stderr.

The existing `_current_endless_session_id() -> int | None` API is **retained as
a thin wrapper** around a new `_resolve_current_session() -> SessionResolution`,
so the ~9 existing callers don't all need updates; only callers that surface
diagnostics (`event_bridge.emit_event`, `session_cmd.session_id_resolve`) switch
to the full form.

The env-var layer (`ENDLESS_SESSION_ID`) is **not validated** — Mike's task
description scoped validation to companion-derived ids. Explicit shell-set env
vars are treated as user intent. (Noted out-of-scope; revisit if env-var
corruption proves real.)

## Files to modify

### `src/endless/task_cmd.py`

1. Add a frozen dataclass at module level (near the resolvers, ~line 1975):
   ```python
   @dataclass(frozen=True)
   class SessionResolution:
       """Outcome of resolving the current Endless session id.

       eid is the resolved id (int) or None on any failure. stale lists the
       companion-file paths whose recorded endless_session_id was not present
       in the sessions table during this resolve — non-empty stale + eid=None
       signals "stale companion blocked resolution" to callers that produce
       user-facing diagnostics. sibling_ambiguous is the count of valid
       sibling matches when >1 (otherwise 0); preserves the existing
       "N sibling panes" caller message.
       """
       eid: int | None
       stale: tuple[str, ...]
       sibling_ambiguous: int
   ```

2. Add a validation helper (near the resolvers):
   ```python
   def _session_id_exists_in_db(eid: int) -> bool:
       """Return True iff eid is a PK in the sessions table."""
       rows = db.query(
           "SELECT id FROM sessions WHERE id = ? LIMIT 1", (eid,)
       )
       return bool(rows)
   ```

3. Refactor `_find_sibling_claude_session()` (currently lines 2017–2048) to
   return `SessionResolution`. Reject-on-any-stale semantics:
   - Walk sibling companion matches as today.
   - Validate each match's `endless_session_id` via `_session_id_exists_in_db`.
   - If **any** match is stale, return
     `SessionResolution(eid=None, stale=(<all stale paths>,),
     sibling_ambiguous=0)` and emit one `click.echo(..., err=True)` line per
     stale path (one-liner naming the path and id).
   - If all valid: apply existing 0 / 1 / many policy. `>1` ⇒
     `SessionResolution(None, (), len(matches))`.

4. Add `_resolve_current_session() -> SessionResolution` that mirrors today's
   three-layer logic in `_current_endless_session_id()` (lines 1976–2014):
   - Layer 1 (env var, unvalidated): hit ⇒
     `SessionResolution(int(env_id), (), 0)`.
   - Layer 2 (pane-direct companion): if a companion matches `TMUX_PANE`:
     - Validate its eid; on success return
       `SessionResolution(eid, (), 0)`.
     - On failure emit the warning line, return
       `SessionResolution(None, (companion_path,), 0)` — **do not** fall
       through to sibling layer (per spec: name corruption, don't hide it).
   - Layer 3 (sibling): call new `_find_sibling_claude_session()`; return its
     `SessionResolution` directly when no pane-direct candidate existed at all
     (companion list had no `TMUX_PANE` match).

5. Reduce `_current_endless_session_id()` to a one-liner wrapper:
   ```python
   def _current_endless_session_id() -> int | None:
       return _resolve_current_session().eid
   ```
   Existing callers in `task_cmd.py` (`task_claim`, `task_bind`,
   `_check_task_ownership`, `spawn_window`) and `session_activity.py` are
   unaffected.

### `src/endless/session_cmd.py`

1. `_find_sibling_claude_session` callers inside `session_id_resolve()`
   (line 1315) and other callers in this file need to pull from
   `.eid` and `.sibling_ambiguous` rather than the old tuple. Mechanical
   migration.

2. `session_id_resolve()` (lines 1283–1329): instead of calling
   `_current_endless_session_id()` then re-deriving the failure cause, call
   `_resolve_current_session()` directly. When `.stale` is non-empty, emit:
   ```
   No current Endless session: stale companion file at <path>;
   recorded endless_session_id is not present in the sessions table.
   Delete the companion file or export ENDLESS_SESSION_ID=<valid-id>.
   ```
   When `.sibling_ambiguous > 0` or no layer matched, keep today's existing
   messages.

### `src/endless/event_bridge.py`

1. `emit_event()` lines 65–80: switch to `_resolve_current_session()` so we
   can inspect `.stale`. When the gate at line 81 would refuse AND
   `resolution.stale` is non-empty, the raised `click.ClickException` appends:
   ```
   A stale companion file was detected at <path>: its recorded
   endless_session_id does not exist in the sessions table. Delete the
   stale companion file (or restart the Claude session) before retrying.
   ```
   The existing generic message (run from Claude pane / export
   ENDLESS_SESSION_ID / bind) is suppressed when stale is the cause, since
   neither remedy fixes a stale file.

## Tests

Add `tests/test_session_id_validation.py` (new file, opts out of
`stub_current_session_id` via `@pytest.mark.no_session_stub` like
`test_session_id.py`).

Use `isolated_env` + `seeded_project_at_cwd` fixtures, write companion files
directly into `<project>/.endless/sessions/claude-<uuid>.json`, and insert /
omit `sessions` rows via `db.execute` to control the DB side.

Test cases:

1. **`_session_id_exists_in_db`** — true for inserted row, false for missing id.
2. **Pane-direct + valid DB row** — `_resolve_current_session()` returns the
   eid with empty `stale`.
3. **Pane-direct + missing DB row** — returns `eid=None`,
   `stale=(<companion_path>,)`. Stderr (capture via `capsys`) contains the
   path and the id. **Does not** fall through to sibling layer (assert by
   placing a valid sibling companion and confirming it's *not* returned).
4. **Sibling single + valid DB row** — returns the eid.
5. **Sibling single + missing DB row** — returns `eid=None` with the path in
   `.stale`.
6. **Sibling 2 valid + 1 stale** — under "reject any stale group", returns
   `eid=None`, all three? no — only the stale path goes in `.stale`. Confirms
   the whole resolution is rejected even though one valid candidate exists.
7. **`endless session id` CLI** — invoke via `CliRunner`, with stale pane-
   direct companion. Asserts exit non-zero, empty stdout, stderr contains
   "stale companion file" and the path.
8. **`emit_event` gate** — call with `actor_kind="cli"` and a stale pane-
   direct companion present; assert `ClickException` raised with stale-aware
   message body. Existing `test_event_bridge.py` patterns (mock subprocess /
   force gate path) carry over.

Update `tests/test_session_id.py` only if any test calls
`_find_sibling_claude_session` directly and asserts the old tuple shape (it
uses `_force_sibling_finder` to monkeypatch, so callers receive whatever the
fixture returns — tests still pass, but the fixture's return values must
become `SessionResolution`s if the function's real signature changes; verify
during implementation).

## Out of scope

- Auto-deleting stale companion files. Mirrors how `_read_live_companions`
  unlinks files with dead PIDs, but the upstream tracks (E-1332/E-1337/E-1391)
  own that cleanup; deleting from inside a read-only resolver risks racing
  the writer.
- Validating the env-var layer. Mike's task description scoped validation to
  companion-derived ids; revisit only if env-var corruption is observed.

## Verification

Manual (matches the task's verification checklist):

1. In a worktree with a real bound Claude session, copy its
   `.endless/sessions/claude-<uuid>.json` aside.
2. Edit the file to set `endless_session_id` to a value that does NOT exist in
   the `sessions` table (e.g., 99999). Save.
3. From the matching pane, run each:
   - `endless session id` → exit non-zero, stderr names the companion path
     and the bad id.
   - `endless session show` → "Session E-99999 not found in database." (this
     message already exists in `session_show_resolve` and now triggers
     because the resolver doesn't silently swap in a sibling).
   - `endless task bind <task-id>` → `ClickException` from the gate, stale-
     companion message body.
   - `endless task update <task-id> --status ready` → same gate refusal
     (exercises `emit_event` path).
4. Confirm none of the commands silently produce a wrong session id, fall
   through to a sibling, or pass through to the gate as if valid.
5. Restore the companion file; confirm all commands work normally.

Automated:
- `just test` (Python pytest suite).
- New tests in `tests/test_session_id_validation.py` cover the resolver and
  CLI surfacing.

## Critical files

- `src/endless/task_cmd.py:1976–2048` — resolver + helper + dataclass
- `src/endless/session_cmd.py:1283–1329` — `session_id_resolve`
- `src/endless/session_cmd.py:1000–1077` — companion machinery (read-only here)
- `src/endless/event_bridge.py:65–90` — `emit_event` gate
- `internal/schema/schema.sql:51–71` — `sessions` table schema (reference only)
- `tests/conftest.py` — `isolated_env`, `seeded_project_at_cwd`,
  `stub_current_session_id` fixtures (used by new tests)
- `tests/test_session_id.py` — existing patterns to mirror
- `tests/test_event_bridge.py` — existing gate-refusal patterns to mirror
