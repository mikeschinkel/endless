# E-1312 — Add `endless session status add` command (XML input)

## Context

Sessions today report status as chat-tables (ephemeral, only Mike-readable). The `session_statuses` table exists in Mike's local DB but isn't in the codebase's schema, and there's no CLI to write to it cleanly — current workflow is `endless sql --write "INSERT ..."` per the temp instruction sheet at `docs/private/TEMP-how-to-insert-session-statuses.md`.

E-1312 formalizes this:
- Land the `session_statuses` schema in the codebase so fresh installs have it.
- Add `endless session status add` that takes XML on stdin (or a file path), parses, dedups against the latest row, inserts, and echoes a markdown rendering back to stdout for chat readability.
- Author interface (XML schema) stays stable across v1 (text-blob storage) and v2 (future `session_status_task` child table) per the design discussion.

## Schema

V7 migration in `internal/monitor/migrate.go`. Extends Mike's existing local schema (which has the original 7 columns) with three new TEXT columns based on feedback from another session that surfaced gaps:

```sql
CREATE TABLE IF NOT EXISTS session_statuses (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  INTEGER,
    headline    TEXT,
    resolved    TEXT,
    pending     TEXT,
    blocked     TEXT,
    verify      TEXT,
    decisions   TEXT,    -- design choices / framings / insights (NEW)
    commits     TEXT,    -- commits-without-task-IDs (NEW)
    memory      TEXT,    -- memory entries created/edited (NEW)
    notes       TEXT,
    created_at  TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS session_statuses_session_recent_idx
  ON session_statuses (session_id, created_at DESC);
```

The migration uses `CREATE TABLE IF NOT EXISTS` so Mike's existing install needs the three new columns added separately. Migration logic:

```go
func migrateV7(db *sql.DB) error {
    // Fresh install or pre-E-1312: create from scratch.
    if !hasTable(db, "session_statuses") {
        db.Exec(`CREATE TABLE session_statuses (...full schema...)`)
    } else {
        // Mike's local install has the original 7 columns; ALTER to add the new 3.
        if !hasColumn(db, "session_statuses", "decisions") {
            db.Exec(`ALTER TABLE session_statuses ADD COLUMN decisions TEXT`)
        }
        if !hasColumn(db, "session_statuses", "commits") {
            db.Exec(`ALTER TABLE session_statuses ADD COLUMN commits TEXT`)
        }
        if !hasColumn(db, "session_statuses", "memory") {
            db.Exec(`ALTER TABLE session_statuses ADD COLUMN memory TEXT`)
        }
    }
    db.Exec(`CREATE INDEX IF NOT EXISTS session_statuses_session_recent_idx
             ON session_statuses (session_id, created_at DESC)`)
    return nil
}
```

Bump `CurrentSchemaVersion` from 6 to 7.

## XML schema

Root: `<session-status>`. All section children are optional; a missing section stores `''` (not NULL).

```xml
<session-status>
  <headline>...one-line summary...</headline>

  <resolved>
    <task id="E-NNN" status="confirmed" filed="true">note</task>
    <task id="E-NNN" status="confirmed">note</task>
  </resolved>
  <pending>
    <task id="E-NNN" status="needs_plan" filed="true">note</task>
  </pending>
  <blocked>
    <task id="E-NNN" status="blocked">waiting on X</task>
  </blocked>
  <verify>
    <task id="E-NNN" status="verify">awaiting confirm</task>
  </verify>

  <decisions>
    <decision>design choice / framing / insight (free text)</decision>
  </decisions>
  <commits>
    <commit sha="1e3bbfc">one-line description (no task ID)</commit>
  </commits>
  <memory>
    <entry path="feedback_no_autonomous_remediation.md">brief summary</entry>
  </memory>

  <notes>free-form prose; catches skipped items, handoffs, etc.</notes>
</session-status>
```

Element/attribute validation (strict; `click.ClickException` on violation):
- `<task>`: `id` must match `E-\d+`; `status` must be one of `needs_plan|ready|in_progress|verify|confirmed|assumed|completed|blocked|revisit|declined|obsolete`. `filed` (optional) must be `"true"` or `"false"`; default `false`.
- `<commit>`: `sha` required; must match `[0-9a-f]{7,40}`.
- `<entry>` (under `<memory>`): `path` required (no further format check — filenames vary).
- `<decision>`: no attributes; element body is the text.

Parsing uses Python stdlib `xml.etree.ElementTree`. Unknown root, missing required attrs, malformed XML → clear error message; no row inserted.

## Storage

For the four task-section columns, serialize each `<task>` element back to XML and join by newlines (matches the temp instruction-sheet rule "one `<task>` per line"). `headline` and `notes` get the element's text content (or `''` if empty). Missing sections store `''`, never NULL.

## Dedup

Before insert, SELECT the latest row for this `session_id`. If all six text columns (headline, resolved, pending, blocked, verify, notes) byte-equal the new content, skip the insert. Echo a one-line message: "no change since last status; skipped." Otherwise insert.

## Session ID discovery (Go-owned)

Per the "no Python SQLite reads in new code" rule, discovery moves to Go. Add a new function in `internal/monitor/tmux_lookup.go` (next to `GetActiveTaskForPane`):

```go
// GetLiveSessionByProcess returns the most-recently-active live session
// whose `process` column matches the given identifier (typically a tmux
// pane id like "%124"). Returns sql.ErrNoRows if no live session exists.
func GetLiveSessionByProcess(process string) (sessionID int64, err error) {
    // SELECT id FROM sessions WHERE process = ? AND state != 'ended'
    // ORDER BY last_activity DESC LIMIT 1
}
```

The column is named `process` (not `pane_id`) because the original design intended harness-agnosticism — tmux pane id is just the current value space, not the only one. The flag and Go function names follow this convention.

`endless-event emit` gains a `--process VALUE` flag (or alternatively, the session-status emit subcommand reads it from a payload field). Python passes `--process "$TMUX_PANE"` when invoking emit. Empty value → Go returns an error which surfaces as a clear "no live session for process ''" message.

A `--session-id N` flag on the Python CLI overrides Go discovery for testing.

## Input shapes

Two accepted forms:
- **Positional `INPUT_FILE` arg**: `endless session status add path/to/status.xml`. Reads file content.
- **Stdin**: `endless session status add < status.xml` or `endless session status add <<EOF ... EOF`. If no arg given, reads from stdin.

Exactly one source: if both an arg AND stdin are given, prefer the arg (stdin is ignored). Empty input → error.

## Markdown echo

After successful insert (or "no change" skip), render the row to markdown on stdout:

```
## Status
<headline text>

## Resolved
| Task | Status | Note |
|---|---|---|
| E-1208 | confirmed | verbs.jsonl write-time commit |
| ...

## Pending
| Task | Status | Note |
|---|---|---|
| ...

## Blocked
(empty)

## Verify
| Task | Status | Note |
|---|---|---|
| ...

## Notes
<notes text>
```

Sections with no content render as `(empty)` so the structure is visible. The chat sees the same content the row holds.

## CLI registration

Subcommand structure follows existing pattern (`session_cmd` group in `src/endless/cli.py:456`):

```python
@session_cmd.group("status")
def session_status_cmd():
    """Record and query session status snapshots."""
    pass

@session_status_cmd.command("add")
@click.argument("input_file", required=False, type=click.Path(exists=True, dir_okay=False))
@click.option("--session-id", "session_id_override", type=int, default=None,
              help="Override tmux-pane session lookup (mostly for testing).")
def session_status_add(input_file, session_id_override):
    """Insert a session status row from XML input."""
    from endless.session_status_cmd import session_status_add as impl
    impl(input_file, session_id_override)
```

Implementation lives in a new `src/endless/session_status_cmd.py` module to keep `cli.py` lean.

## Live-DB pre-step

✓ Done by Mike before this plan exits. Local DB has the three new columns + the `(session_id, created_at DESC)` index. The Go V7 migration in `internal/monitor/db.go` uses idempotent guards (`hasColumn` / `IF NOT EXISTS`) so re-running on Mike's already-altered DB is safe; fresh installs get everything created from scratch.

## Files to modify

**Go (writes + discovery + render):**
- `internal/monitor/migrate.go` — append `{Version: 7, Name: "session_statuses table (E-1312)", Apply: migrateV7}` to migrations slice. Bump `CurrentSchemaVersion` to 7.
- `internal/monitor/db.go` — add `migrateV7` with idempotent CREATE TABLE / ALTER ADD COLUMN / CREATE INDEX (idempotent vs Mike's already-ALTERed local DB).
- `internal/monitor/tmux_lookup.go` — add `GetLiveSessionByProcess(process string) (int64, error)`.
- `internal/events/kinds.go` — add `KindSessionStatusRecorded = "session_status.recorded"` and register in `ValidKinds`.
- `internal/events/execute.go` (or wherever Execute dispatches by kind) — add handler: resolve session via `GetLiveSessionByProcess`, dedup check (SELECT latest row, byte-compare), INSERT, return rendered markdown.
- `internal/events/render.go` (new, or fold into the handler) — `RenderSessionStatusMarkdown(row SessionStatusRow) string`.
- `cmd/endless-event/main.go` — emit flow for the new kind: pull `headline`/`resolved`/...payload fields, call Execute, return its rendered markdown in the stdout JSON output.

**Python (parse + invoke + display):**
- `src/endless/cli.py` — register `session status` subgroup and `add` command. Forward to `session_status_cmd.session_status_add`.
- `src/endless/session_status_cmd.py` (new) — XML parser, validator, payload builder; shells to `endless-event emit --kind session_status.recorded --process $TMUX_PANE --payload <json>`; reads the rendered markdown from emit's stdout; prints to chat.
- `src/endless/event_bridge.py` — minor: ensure the new kind's emit invocation shape (entity-type, entity-id, etc.) is accommodated. May need to thread `--process` through.

**Tests:**
- `internal/events/session_status_test.go` (new) — Go-side handler tests: insert path, dedup-skip path, validation, multi-line task body rendering, `filed="true"` attribute pass-through, missing-section → empty string.
- `tests/test_session_status_add.py` (new) — Python-side CliRunner tests: XML parsing happy path, file arg vs stdin, validation errors, shells-out-correctly to endless-event, displays markdown.

## Tests to add

- **Schema:** existing Go migration tests cover V7 if any. If none, add a smoke test that asserts `session_statuses` table exists after auto-migrate.
- **CLI: happy path** — valid XML on stdin → row inserted; SELECT confirms columns; stdout has the markdown render.
- **CLI: file arg** — same input from a file path; same result.
- **CLI: dedup** — insert the same XML twice; second invocation reports "no change" and HEAD count is 1 not 2.
- **CLI: empty sections** — XML omits `<blocked>`; row's `blocked` column is `''` (not NULL).
- **CLI: validation errors** — missing root `<session-status>`, missing `<task id>`, invalid `status` enum → ClickException with descriptive message; nothing inserted.
- **CLI: session id discovery** — happy path with `TMUX_PANE` set + a seeded session row; override flag bypasses the lookup; "no live session" error when both lookup and override fail.
- **CLI: markdown render** — task tables formatted; empty sections render `(empty)`.

## Verification (post-land)

1. **Real session**: from this Claude pane, `endless session status add <<'EOF' ... EOF` should insert a row and echo markdown. SELECT confirms.
2. **Round-trip**: insert via CLI, then read via `endless sql "SELECT * FROM session_statuses WHERE id = (SELECT MAX(id) ...)"` — values match.
3. **Dedup**: re-run with identical content; CLI says "no change; skipped"; row count unchanged.
4. **Old install**: simulate by deleting `_schema_version` row for V7; rerun `endless` (auto-migrate); table re-created via `IF NOT EXISTS`; no data loss.

## Out of scope

- `session_status_task` child table (deferred per E-1312 scope; v1 stores task XML as text blobs).
- `endless session status list` / `show` (read commands). Add when read needs become concrete.
- HTML rendering for web UI; just markdown for now.
- A Stop-hook that auto-fires `add` based on chat content. Try natural-call first per Mike's earlier preference.

## Resolved during planning

- **Format**: XML (custom-element style, parsed by `xml.etree.ElementTree`).
- **Stdin or file**: both accepted; positional arg takes precedence if given.
- **Echo**: markdown rendering on stdout after successful insert/skip.
- **Dedup**: byte-equal check on all text columns; skip if identical.
- **Storage**: each `<task>` element on its own line within the section column (matches the temp instruction sheet convention).
- **Schema migration version**: V7 (bumps from V6).
- **Schema expansion**: +3 TEXT columns (`decisions`, `commits`, `memory`) + `filed="true"` attribute on `<task>`, based on feedback from another session about categories that landed in `notes` for lack of a structured field. Skipped items + cross-session handoffs stay in `notes` for v1; promote later if patterns recur.
- **Index**: add `(session_id, created_at DESC)` for dedup-query speed.
- **Live-DB step**: manual ALTERs first, then code change. Captured above.
- **Multi-line task body rendering**: replace `\n` with `<br>` in markdown table cells.
