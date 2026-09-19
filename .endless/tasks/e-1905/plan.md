# Plan — E-1905: remove vestigial `transcript_path` + `session reimport`

Both are vestigial. `session_messages` is kept current by the LIVE Go parser
(`ParseTranscript`, `transcript.go:304`) on every event using Claude's payload
path; the stored `transcript_path` column feeds essentially nothing and goes
stale when a session changes cwd after start (recorded once at SessionStart,
never re-recorded). `session reimport` is a manual-only CLI escape hatch with a
duplicate Python parser; nothing automatic needs it. Decision (this session):
remove `session reimport` as well (option A), not keep a discovery-based form.

KEEP `transcript_offset` — it is the live parser's resume cursor
(`transcript.go:241/252`), unrelated to `transcript_path`.

## 1. Remove `session reimport` (command + Python parser)

- `src/endless/cli.py:1060-1065` — delete the `reimport` command + wiring.
- `src/endless/session_cmd.py` — delete `reimport_sessions` (~849), the duplicate
  `_parse_transcript_py` (~1202) and its helpers, and `_find_jsonl` (~857 — used
  ONLY by reimport). Remove any now-dead imports.
- Drop reimport-specific tests.

## 2. Drop the `transcript_path` column

- Schema: remove `transcript_path TEXT` from `internal/schema/schema.sql:137` and
  the mirrored DDL/backfill in `src/endless/db.py` (~361).
- Add a schema change under `internal/schema/changes/` that drops the column,
  following the `e-1568` sessions-rebuild pattern (SQLite `ALTER TABLE ... DROP
  COLUMN`, or table rebuild if the version in use needs it). The `sessions` table
  is machine-local (not journaled to the shared ledger), so this is a local
  schema change — no ledger upcast needed.

## 3. Remove writers/readers of the column

- `internal/hookcmd/claude.go:194-197` — delete the SessionStart
  `SetTranscriptPath(payload.SessionID, payload.TranscriptPath)` block (and the
  "Store transcript path for reimport" comment). `payload.TranscriptPath` is still
  used live by the `ParseTranscript` calls (232/252/262/265) — leave those.
- `internal/monitor/transcript.go` — delete `SetTranscriptPath` (~319) and
  `GetTranscriptPath` (~331, already caller-free) plus their round-trip tests
  (`transcript_test.go:397-419`).
- `internal/events/reopen_context.go` — in `inheritedSessionID`'s ORDER BY, drop
  the `OR transcript_path IS NOT NULL` evidence clause (leave `process IS NOT NULL`
  and the `>= 10`s span). Update the doc comment (~41) that names transcript_path
  as an evidence signal. Minor, intended behavior change: transcript_path is no
  longer a tiebreak signal.
- `src/endless/session_cmd.py:69/81/91` — remove `transcript_path` from the SELECT
  column lists; drop the `:854` read (goes with reimport).

## Verification — tests/tasks/e-1905-verify.sh (fully scripted)

- Build + `go test ./...` + `just test` green.
- `endless session reimport` is gone (command not found / not registered).
- `session list`, `session search`, and recaps still work — `session_messages` is
  unaffected (still populated by the live Go parser); assert a live-parsed session
  still yields messages/search hits against the sandbox.
- No remaining references to `transcript_path` in `internal/` or `src/` (grep guard).

## Boundary

- Does NOT touch `transcript_offset` or the live `ParseTranscript` path.
- Not a plan-file/plan-reimport change — that is a separate, already-abandoned
  concept; this is only session transcript tracking.
