You are implementing **E-1393** — first narrative-kind carve-out from `session_statuses` JSON into a dedicated table. Sets the pattern for subsequent narrative kinds (observations, runbooks, etc. via E-1394 and beyond). Implements E-1352 (events-as-primitive); follows E-1322's commit `4652b1a` for the table+handler pattern.

## What this is

Today, `endless session status add` parses an XML payload with multiple aspects bundled into one row of `session_statuses`. The `decisions` aspect lands in `session_statuses.decisions` as a JSON array. Under E-1352's events-as-primitive direction, decisions become their own first-class kind:

- New event kind: `session.decision_recorded`
- New table: `session_decisions(session_id, decision_text, created_at)`
- New CLI: `endless session decision add "<text>"`

The existing batch wrapup XML continues to work; the handler should fan out the `<decision>` elements into `session_decisions` rows (one per element) in addition to (or instead of) populating `session_statuses.decisions`. **Discuss at plan time**: dual-write (both old JSON column + new table) for backward compat, or new-table-only (eventually drop the column). Pick the simpler path that doesn't break existing reads.

## Schema (V12 migration)

```sql
CREATE TABLE session_decisions (
    id           INTEGER PRIMARY KEY,
    session_id   INTEGER NOT NULL,
    decision_text TEXT   NOT NULL,
    created_at   TEXT    NOT NULL
);
CREATE INDEX idx_session_decisions_session ON session_decisions(session_id, created_at DESC);
```

This one DOES get a surrogate `id` PK — decisions are append-only events with no natural compound key (the same session can record many decisions; the text isn't a key). Unlike session_tasks where the (session, task) pair was naturally unique.

NO FK on session_id per the materialized-index-outlives-entities rationale.

## Event kind

`internal/events/event.go`:
```go
KindSessionDecisionRecorded Kind = "session.decision_recorded"
```

Payload:
```go
type SessionDecisionRecordedPayload struct {
    Text string `json:"text"`
}
```

## Handler

`internal/events/executor.go`: `execSessionDecisionRecorded`. INSERT one row into session_decisions.

Plus: in the existing `execSessionStatusRecorded` handler, when parsing the wrapup XML's `<decisions>` element, ALSO emit one `session.decision_recorded` event per `<decision>` child (or directly INSERT — the simpler path; emitting from within a handler is layering work that may not be worth it). The cleanest path is probably: in execSessionStatusRecorded, after the parent row insert, loop the decisions and INSERT into session_decisions directly. The events JSONL gets just one `session_status.recorded` event (its existing shape); the materialized fan-out lives in the handler.

## CLI surface

```
endless session decision add "<text>"

Options:
  --session N    Session id (default: current pane)
```

Output: confirmation + decision id.

Plus the symmetric read:

```
endless session decisions [OPTIONS]

Options:
  --session N    Session id (default: current pane)
  --since <when>
  --limit N
  --format [table|json]
```

Output: id / created_at / decision text (truncated for table view).

The `add` verb writes via event emit (consistent with other writes); the read goes through a Go monitor function (E-894).

## Files to touch

- `internal/monitor/db.go` — migrateV12 + version bump
- `internal/events/event.go` / `payload.go` — new Kind + payload
- `internal/events/executor.go` — execSessionDecisionRecorded + dispatch case + fan-out in execSessionStatusRecorded
- `internal/events/session_decisions_test.go` (new) — handler + fan-out tests
- `internal/monitor/session_decisions.go` (new) — read helper
- `src/endless/session_cmd.py` — `session decision add` + `session decisions` subcommands

## Pattern for subsequent narrative kinds

This task SETS THE PATTERN. Subsequent narrative kinds (observations via E-1394; runbooks/conclusions if/when filed) should follow the same shape:

- Surrogate id PK (these are append-only event-rows, no natural compound key)
- INSERT-only handler (no UPSERT; each event is a discrete record)
- `add` write verb + plural read verb (`decisions` listing for the singular `decision`)
- Fan-out hook from `execSessionStatusRecorded` if the wrapup XML carries the kind

Document this pattern in the commit message so E-1394's implementer can mirror it without rediscovery.

## Critical: actor guard

For execSessionDecisionRecorded: same guard as session_tasks: `evt.Actor.SessionID != ""`. No `Kind == ActorSession` check. See LESSONS.md "Verify the actor model before specifying actor-based guards."

## E2E verification (critical)

```
XDG_CONFIG_HOME=$HOME/.config endless session decision add "test decision for E-1393"
XDG_CONFIG_HOME=$HOME/.config endless sql "SELECT * FROM session_decisions ORDER BY id DESC LIMIT 1"
# One row, recent created_at, text matches
XDG_CONFIG_HOME=$HOME/.config endless session decisions
# Lists the decision

# Then verify the wrapup XML fan-out (heavier; only if you keep the dual path):
# Submit an XML wrapup with two <decision> elements, check session_decisions has two new rows.
```

## House rules

- `just build`. ClearPath. doterr. go-dt.
- In-tx dbQuerier pattern.
- New CLI subcommand names aren't title-verb gated.

## Closing

- `endless task update E-1393 --status verify --outcome "session_decisions table + decision_recorded event + add/list CLI verbs; wrapup XML fans out (or describe the chosen path); pattern documented for narrative-kind followups."`
- Commit message MUST document the carve-out pattern (subsequent narrative kinds will reference it).
- Handoff: "To verify: `endless session decision add 'test'`, then `endless session decisions` shows it."
- Do NOT auto-land. Ask Mike.
