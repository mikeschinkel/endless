# Plan — remove the event kinds that have no executor or emitter

Decided with Mike, 2026-10-18 (E-2402 /needs-plan: Q1 = B, Q2 = A; delete outright rather than retire).

## Remove the dead kinds

In `internal/events/event.go`, delete these 18 kinds from the `Kind` constants and from `ValidKinds`:

- `session.work_started`, `session.chat_started`, `session.idled`, `session.ended`, `session.task_completed`, `session.hidden`
- `project.registered`, `project.updated`, `project.renamed`, `project.unregistered`, `project.purged`
- `note.created`, `note.resolved`
- `conversation.beaconed`, `conversation.connected`, `conversation.closed`
- `message.sent`, `message.delivered`

In `internal/events/payload.go`, delete their payload structs (`SessionWorkStartedPayload` … `NoteResolvedPayload`). They are deleted, not added to `RetiredKinds`: no Endless ledger on Mike's machine holds any of them (checked across every project's `.endless/db-ledger`), and there are no other users yet.

Update what referenced them: the payload round-trip cases in `internal/events/event_test.go`, and any `ValidKinds` count or listing in `internal/eventcmd` and its tests. Find the sites by searching for each removed constant and kind string.

`epic.status_derived` stays: it has no executor case, but the projector replays it and `internal/eventcmd` emits it.

## Guard against new dead kinds

Add a Go test in `internal/events` that fails when a `ValidKinds` entry has neither a `case` in the executor's dispatch switch nor a `case` in the projector's replay switch. It reads the case labels from `executor.go` and `projector.go` with `go/parser`, so it needs no database, and its failure message names each uncovered kind.
