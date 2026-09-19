# Plan — retire four abandoned surfaces in one pass

Three from this task's analysis (the curated `next` list, `task import`, session
task ordering), plus `tasks.source_file`, which only `task import` ever
populated meaningfully.

## Decisions (Mike, 2026-09-19 — settled)

1. **Retired event kinds replay as declared no-ops.** The ledger may hold
   `project_next.revised`, `task.bulk_cleared` and `session_tasks.ordered`. A
   named retired-kinds list, which the validator and projector accept and do
   nothing for, keeps replay working and the retirement visible in code — while a
   kind nobody declared still fails loudly, which is the property worth keeping.
2. **`session status --tree` stays** (the 2026-09-14 decision stands, re-confirmed
   now that E-2164 is planned). Only its ordering override goes.
3. **The `task import` removal reaches the column**: the command, the bulk-clear
   path it is the only caller of, and `tasks.source_file` itself.
4. **One land.** All four removals share the retired-kinds decision; splitting
   means making it once and applying it across three reviews.

## 1. Retired kinds first

Add the retired-kinds registry beside the kind validation in
`internal/events/event.go` and honour it in the projector: the three kinds above
validate, project nothing, and are named in one place with a comment saying which
task retired them. Everything below can then delete freely.

A dropped COLUMN has the same shape: historical `task.created` payloads carry
`source_file`, and the payload struct keeps the field so old events still
unmarshal — the executor and projector simply stop writing it.

## 2. The curated `next` list

- Command `task next revise` — a subcommand of `task next`, which STAYS: it is the
  live "what should I do next" query and is unrelated.
- Tables `project_next`, `project_next_lanes`, `project_next_tasks`,
  `project_next_pending`, `project_next_events`.
- Go: `internal/events/project_next.go`,
  `internal/events/project_next_pending.go`, and the project-next branches in
  `internal/events/event.go`, `internal/events/executor.go`,
  `internal/eventcmd/event.go`, `internal/monitor/project_path.go`.
- Python: the project-next reads in `task_cmd`.

## 3. `task import` and `source_file`

- The `task import` command, and `task import-json`, which writes the sentinel
  `source_file` value `json_import`.
- The bulk-clear removal path: `removeTasksBySourceFile` in
  `internal/events/task_removal.go` (no other caller once the command is gone),
  its `task.bulk_cleared` kind, and `_refuse_bulk_clear_with_relations` in
  `task_cmd` with the relation guard that exists only for it.
- The column `tasks.source_file` and its real readers: the INSERT in
  `internal/events/executor.go` and its twin in `internal/events/projector.go`,
  the `SourceFile` writes in `internal/events/payload.go` (field retained for
  historical unmarshalling, no longer written), the `Source:` line in `task show`,
  and the `source_file` key in the task export/JSON shape.
- **Do not touch three false matches**: `config.source_file` is a re-exec
  re-entrancy parameter, the `cli` mention of it is a comment about that, and the
  `decision_cmd` mention is a comment noting decisions have no such field. None
  of them is this column.
- Verify nothing else shells `task import` before deleting — a setup or migration
  path plausibly does.

## 4. Session task ordering

- `endless session order`, the only writer of `session_tasks.do_order`.
- The column `session_tasks.do_order`.
- `monitor.SessionStatusDoOrder` and its query.
- In `internal/sessionstatuscmd/tree.go`: `buildForest`'s `doOrder` branch and
  `assignByLayer`, so the tree always derives from the DAG. `--tree` itself stays.
- The do_order handling in `internal/events/session_tasks.go` and the membership
  carry-over in `internal/events/session_task_membership.go`.

## 5. One schema change

A single `.go` change under `internal/schema/changes/`, following the two recent
column-drop precedents there: five tables and two columns
(`session_tasks.do_order`, `tasks.source_file`).

**It cannot be validated by rebuilding.** Rebuild is on hold until after E-894,
so the retired-kinds behaviour is proven by a unit test that replays a fixture
ledger containing all three kinds, not by a real rebuild.

## 6. Docs

Update the tasks and sessions guide sections that describe `task import`,
`task next revise` and `session order`, then regenerate the command
cross-reference. A retired command left in the guide is a promise the binary no
longer keeps.

## Interaction with E-2159

E-2159 converts every refusal site in the codebase, and this task deletes several
of them outright — so landing this first means E-2159 does not convert messages
that then disappear. That is an advisory ordering, not a blocking one: nothing
breaks if they land the other way round, it only wastes the conversion.

## Verification

- `endless task next` still works; `task next revise`, `task import`,
  `task import-json` and `session order` are gone from `--help` and refuse as
  unknown commands.
- `session status --tree` still renders, ordering derived from the DAG, with no
  reference to do_order.
- `task show` no longer prints a `Source:` line; the JSON shape no longer carries
  `source_file`.
- A fixture ledger containing `project_next.revised`, `task.bulk_cleared` and
  `session_tasks.ordered` replays clean; a ledger containing a genuinely unknown
  kind still fails with the unknown-kind error.
- The schema change applies to a copy of a real database, and `endless sql`
  confirms the five tables and two columns are gone.
- `just test` and `just test-go` pass; the guide cross-reference regenerates with
  no stale rows.
