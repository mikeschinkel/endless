# E-1101: Validate phase enum at events/projector boundary

## Problem

Phase is enumerated in practice (`now`, `next`, `later`, `maybe` after E-1088),
but the Go events/projector layer accepts any string. SQL CASE expressions
silently sort unknown values under `ELSE`, masking bad data instead of
surfacing it. A malformed event or direct write could land an arbitrary
phase string in the projection.

Single source of truth for the enum lives in two Python places (the four
`click.Choice` lists in `cli.py` and the heading map in `task_cmd.py`). Go
had no source of truth at all.

## Fix

New `internal/events/phase.go`:

```go
var ValidPhases = map[string]bool{
    "now": true, "next": true, "later": true, "maybe": true,
}

func ValidatePhase(s string) error {
    if !ValidPhases[s] {
        return fmt.Errorf("events: invalid phase %q (valid: now, next, later, maybe)", s)
    }
    return nil
}
```

Called at every event-ingest boundary that accepts a phase value:

- `executor.go::execTaskCreated` — before INSERT
- `executor.go::execTaskImported` — before INSERT
- `executor.go::execTaskFieldsUpdated` — when phase is in the fields map
- `projector.go::replayTaskCreated` — before INSERT
- `projector.go::replayTaskImported` — before INSERT
- `projector.go::replayTaskFieldsUpdated` — when phase is in the fields map

Both executor (live event creation) and projector (replay path) reject. Defense
in depth: the executor blocks bad data from entering the log; the projector
catches anything that may have slipped in via direct file edit (the events log
is human-readable JSONL).

## Sort-CASE behavior unchanged

The SQL `ELSE` slot in phase-sort CASE expressions stays — it is the
"shouldn't happen" branch now, not the "silently accept anything" branch. With
ingest validation in place, anything reaching `ELSE` represents a corruption
that warrants the fallback rather than a write error.

## Out of scope

- Migrating the Python-side enum into a shared source of truth. Today the
  four `click.Choice` lists are the human-input boundary; Go is the
  events-layer boundary. Each enforces its own surface; cross-language
  enum sharing is a separate concern.
- Auditing existing event logs for invalid phase values. If any exist, they
  are pre-existing data and out of scope for this defensive fix.

## Verification

`internal/events/phase_test.go`:

1. `ValidatePhase` accepts `now`, `next`, `later`, `maybe`.
2. Rejects empty, unknown, and case-variants (`NOW`, `Now`).
3. `ValidPhases` has exactly the four expected keys.

`go test ./internal/events/...` passes (1 package); full Python suite
(167 tests) still passes.
