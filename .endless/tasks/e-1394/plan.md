You are implementing **E-1394** — `session_observations` table + append verb. New narrative kind for "what the agent noticed but isn't yet shaped as a task." Implements E-1352; follows the carve-out pattern established by E-1393.

## Dependency note

E-1393 (decisions carve-out) SETS THE PATTERN this task follows. Verify E-1393 is `confirmed` (or at minimum `verify`) BEFORE writing this. If E-1393's commit message documented the pattern, mirror it precisely — don't diverge. If E-1393 isn't merged yet, surface that and stop; this task's value is partly in pattern consistency.

## What this is

Observations are the catch-all for "I noticed X" — bug patterns, generalizable lessons, inconsistencies, things worth flagging without filing an actionable task. Distinct from decisions (which choose between alternatives). Distinct from tasks (which commit to doing work).

Today: observations get crammed into `session_statuses.notes` as prose, where they're invisible to SQL queries and never surface again. E-1394 gives them a structured home.

## Three pieces

1. **New event kind**: `session.observation_recorded`
2. **New table**: `session_observations(id, session_id, observation_text, created_at)`
3. **New CLI**: `endless session observation add "<text>"` + `endless session observations` read

## Schema (V13 migration)

```sql
CREATE TABLE session_observations (
    id               INTEGER PRIMARY KEY,
    session_id       INTEGER NOT NULL,
    observation_text TEXT    NOT NULL,
    created_at       TEXT    NOT NULL
);
CREATE INDEX idx_session_observations_session ON session_observations(session_id, created_at DESC);
```

Mirror E-1393's session_decisions schema shape exactly (surrogate id PK; no FK on session_id; same index pattern). Symmetry across narrative-kind tables matters for E-1319's eventual aggregated read CLI.

## Event kind

```go
KindSessionObservationRecorded Kind = "session.observation_recorded"

type SessionObservationRecordedPayload struct {
    Text string `json:"text"`
}
```

## Handler

`execSessionObservationRecorded` — same shape as `execSessionDecisionRecorded` from E-1393. INSERT one row when `evt.Actor.SessionID != ""`.

If the wrapup XML grows an `<observations>` aspect (it doesn't have one today), the fan-out hook in `execSessionStatusRecorded` should mirror E-1393's decision fan-out. For this task: don't add the XML element yet — `<observations>` is a forward-looking element worth filing as its own follow-up if Mike wants the wrapup XML to carry observations.

## CLI surface

```
endless session observation add "<text>"     # write
endless session observations                  # read (--session, --since, --limit, --format)
```

Same flag shape as `session decisions` from E-1393.

## Files to touch

- `internal/monitor/db.go` — migrateV13 + version bump
- `internal/events/event.go` / `payload.go` — new Kind + payload
- `internal/events/executor.go` — execSessionObservationRecorded + dispatch case
- `internal/events/session_observations_test.go` (new) — handler test
- `internal/monitor/session_observations.go` (new) — read helper
- `src/endless/session_cmd.py` — `session observation add` + `session observations` subcommands

## Critical: actor guard

Same as E-1393: `evt.Actor.SessionID != ""`. No `Kind == ActorSession`. See LESSONS.md.

## E2E verification (critical)

```
XDG_CONFIG_HOME=$HOME/.config endless session observation add "Noticed E-1322 had a hidden guard bug — checking actor.kind distribution before specifying guards is now in LESSONS.md."
XDG_CONFIG_HOME=$HOME/.config endless sql "SELECT * FROM session_observations ORDER BY id DESC LIMIT 1"
# One row, text matches
XDG_CONFIG_HOME=$HOME/.config endless session observations
# Lists the observation
```

## House rules

- `just build`. ClearPath. doterr. go-dt.
- In-tx dbQuerier pattern.

## Closing

- `endless task update E-1394 --status verify --outcome "session_observations table + observation_recorded event + add/list CLI verbs; mirrors E-1393 carve-out pattern; e2e verified."`
- Handoff: "To verify: `endless session observation add 'test'`, then `endless session observations` shows it."
- Do NOT auto-land. Ask Mike.
