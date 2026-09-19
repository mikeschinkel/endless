# Persist session reports as queryable DB rows

Capture every `endless task report` invocation (E-1771) as normalized,
queryable rows so future tooling can quantify handoffs — never a serialized
blob. E-1771 already computes the facts into a struct and gates the free-text;
this task adds the persistence path and a read surface. The data model is
settled (see --analysis).

## Schema (new tables, added to `internal/schema/schema.sql` directly)

Unshipped software → no migration file, no back-compat; extend the baseline.

```
report            (id, task_id, session_id, status_id, created_at)
report_task_ref   (id, report_id, task_id, role_id, relation_id NULL, status_at_report_id)
report_note       (id, report_id, kind_id, text, gate_verdict_id)
report_question   (id, report_id, text, answer_id NULL, type_id, style_id NULL, gate_verdict_id)
```

House conventions (see memory / existing schema):
- FK columns are separate `_id` integer columns; no inline TIMESTAMP types.
- **No CHECK constraints for enums** — each enum (`role`, `kind`, question
  `type`/`style`/`answer`, `gate_verdict`, and reuse the existing task-`status`
  values table for `status`/`status_at_report`) is an integer FK to a seeded
  values table. Add the Go enum trio (int consts + `String()` slug + `Parse()`)
  per the house pattern; the seed rows go in schema.sql alongside the table.
- `report.task_id` / `report_task_ref.task_id` FK `tasks(id)`;
  `report.session_id` FK `sessions(id)`.

Related tasks are FK'd, not duplicated: `report_task_ref` snapshots only
`status_at_report` (the field whose historical value we query); live-join the
stable detail from `tasks`. Anomalies get NO table (agent-only, transient).

## Persistence wiring (event-sourced, no Python DB write)

E-1771's `report_cmd.report_item` computes facts (Go `session-query
task-report`) + gates notes/questions, then renders the prompt. Add: after the
gate passes, Python emits a `report.created` event (via `event_bridge.emit_event`)
carrying `{task_id, session_id, status, task_refs[], notes[], questions[]}`; a
new Go executor handler in `internal/events/executor.go` writes the `report`
row + child rows in one transaction. This keeps all DB writes in Go (house
rule) and puts reports in the ledger like every other entity.

- Resolve `session_id` from the current session (the same resolver the hook /
  session-query path uses); null when unresolvable rather than failing the report.
- Persistence must not break the report: a write failure logs and still prints
  the steering prompt (the report is the user-facing contract; capture is
  best-effort, same spirit as the fail-open gate).

## Query surface (the one open design decision)

How tooling reads these back — resolve during implementation:
- Minimal: a `session-query reports --task-id N` / `--session-id N` returning
  JSON, mirroring the existing session-query handlers (enables `session status`
  and ad-hoc analysis without a new user command).
- Fuller: an `endless report list/show` user command.
Recommendation: start with the `session-query` JSON read (internal, composable);
add a user command only when a concrete consumer needs it.

## Tests

- Go executor test: emit a `report.created` payload → assert the report +
  child rows (counts, FK values, enum ids) land correctly; unresolved session →
  null session_id, not an error.
- Go read test for the `session-query reports` handler.
- Python: `task report` persists a row on success; a forced write failure still
  prints the prompt (best-effort).
- `tests/tasks/e-1777-verify.sh` (isolated env): run `task report` with and
  without a payload, then query the rows back and assert shape.

## Scope boundary
- Test-result `<tests>` capture (CTRF) rides with this work only if trivial;
  otherwise a follow-up. Not required for the first cut.
- Regression-diffing against a last-green baseline is E-1778 (needs this history).

## Verify
`esu && ./tests/tasks/e-1777-verify.sh`
