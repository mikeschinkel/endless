You are working on **E-1318** (Convert session_statuses narrative columns to JSON).

**Scope** (narrowed from original): convert ONLY the narrative columns (`decisions`, `commits`, `memory`, `summary`) from XML-as-text to JSON. The `tasks` column / `session_status_tasks` child table is OUT of scope — that's handled by a separate task (auto-capture filing table).

The "Author interface stays XML on input" line is load-bearing: this changes STORAGE only, not the CLI's input contract. Python parses XML into JSON before emitting the payload; Go inserts JSON; renderer reads JSON.

## 1. Get oriented

```
endless guide
endless guide sessions
endless task show E-1318 --text
endless task show E-1312                       # original CLI ship — context
endless task show E-1314                       # prior schema change — pattern
endless task show E-1315                       # deadlock fix — relevant to your handler edits
endless task show E-968                        # parent epic
endless task claim E-1318
```

Read the existing implementation:

- `internal/monitor/migrate.go` — migration registry; bump `CurrentSchemaVersion` to 9
- `internal/monitor/db.go` — `migrateV7`/`V8` patterns to follow
- `internal/events/payload.go` — `SessionStatusRecordedPayload` struct (DON'T touch the `Tasks` field in this slice; another task owns it)
- `internal/events/session_status.go` — handler + renderer (per-column reads)
- `src/endless/session_status_cmd.py` — XML parser; emit JSON for narrative columns
- `tests/test_session_status_add.py` — Python tests; will need updates
- `internal/events/session_status_test.go` — Go tests; will need updates

Check schema state:

```
endless sql "SELECT sql FROM sqlite_master WHERE name='session_statuses'"
endless sql "SELECT count(*), max(id) FROM session_statuses"
```

## 2. Do the work

Enter plan mode.

Per-column JSON shape (recommended):

- `decisions`: JSON array of `{"text": "..."}` objects
- `commits`: JSON array of `{"sha": "...", "description": "...", "branch": "..."}` (preserve optional `branch` attribute)
- `memory`: JSON array of `{"path": "...", "summary": "..."}` objects
- `summary`: JSON array of `{"name": "...", "files": "...", "purpose": "..."}` objects

Implementation sequence:

1. Plan-mode discussion with Mike to confirm the JSON object shapes per section.
2. ALTER live DB (read existing rows, parse XML, re-write as JSON in place, COMMIT). Per Mike's pattern: live DB first, then code.
3. Add `migrateV9` (idempotent for fresh installs + Mike's already-altered DB).
4. Update Python parser: build JSON objects for the 4 narrative sections instead of XML strings.
5. Update payload struct field types if needed (still TEXT carrying JSON, so probably no struct change).
6. Update Go handler: dedup-check compares JSON strings (canonical-form? simple byte-compare on what Python emitted?).
7. Update renderer: parse JSON and produce the existing markdown tables.
8. Update tests on both sides.
9. Verify with `endless session status add` and `endless sql "SELECT json_extract(decisions, '$[0].text') FROM ..."`.

Existing rows (5 of them): each has XML in the 4 narrative columns. The migration must parse the XML and re-emit as JSON. Row 1 has empty narrative columns — easy. Rows 2-5 have real content.

**NOT in scope:**
- `tasks` column — leave as-is for this task (separate task handles it).
- `session_status_tasks` child table — not created by this task.
- Read CLI (`list`/`show`/`latest`) — E-1319.

## 3. Closing

When implementation is complete:
- `endless task update E-1318 --status verify` with how-to-test commands
- Dogfood: record a session_status row via the CLI; SELECT-with-json_extract to demonstrate queryability of the new shape.

Don't run `endless worktree land` without asking Mike. Don't mark `confirmed` yourself — that's Mike's call. Use `assumed` if confident but can't easily verify.

Goal: the 4 narrative columns hold valid JSON arrays of objects; SQLite JSON1 queries work against them; the renderer's markdown output is unchanged from the agent's perspective; existing rows are migrated cleanly.
