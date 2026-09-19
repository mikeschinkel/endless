# E-1088: Add 'maybe' phase to task phases

## Context

Phases today are `now`, `next`, `later`. The user wants a fourth phase `maybe`
to record considered-but-undecided ideas — features we may or may not do, where
the choice should be remembered so we don't keep relitigating it.

`later` implies eventual commitment. `maybe` does not.

## Decision (E-1089)

- Add `maybe` as a fourth phase value alongside `now`, `next`, `later`.
- Promotion path is typically `maybe` → `now` or `maybe` → `next` (skipping
  `later`).
- Convention: when conditional (revisit if X), include a `**Trigger:**` line in
  the plan body that names the trigger.
- Pruning of stale `maybe` items is deferred — they accumulate; a future task
  will consider periodic pruning.

## Scope

The phase value is a free-form string in the events/projector layer (no Go
enum), so changes are concentrated in the Python CLI, the Python heading-map,
and the SQL phase sort orders (Python + Go).

## Changes

### Python — `src/endless/cli.py`

Add `"maybe"` to four `click.Choice(["now", "next", "later"])` lists:

- Line 474 (`task list --phase`)
- Line 550 (`task next --phase`)
- Line 614 (`task search --phase`)
- Line 713 (`task update --phase`)

Update help text on lines 645 and 714 to include `maybe`.

### Python — `src/endless/task_cmd.py`

1. `_phase_for_heading` (line 291): add `"maybe": "maybe"` and a couple of
   plausible aliases (`"considering"`, `"tentative"`).
2. Phase sort `CASE` expression (line 739): extend to
   `WHEN 'now' THEN 0 WHEN 'next' THEN 1 WHEN 'later' THEN 2 WHEN 'maybe' THEN 3 ELSE 4 END`.
3. Same for the `next_tasks` ORDER BY (lines 916–917).

### Go — `internal/web/queries.go`

Extend the phase sort `CASE` expression (lines 180–185) the same way.

### Docs — `docs/guide-2026-04-15-using-endless-in-sessions.md`

Add a `maybe` row to the Task Phases table at line 226.

### Validation that nothing else needs updating

- Go `internal/events/*.go`: phase is a free-form `string`; no enum to extend.
- `templ` templates: only check `item.Phase != "now"`; no enumerated phase
  list, so they keep working.
- Web queries `GetProjectTaskGroups` does not sort by phase.

## Sort-order rationale

`maybe` sorts AFTER `later` (slot 3) because items not committed to should
appear below items that are committed-but-deferred. The `ELSE 4` slot still
catches anything unexpected so future phases don't silently sort first.

## Verification

1. `just build` succeeds (Go + Python).
2. `endless task list --phase maybe` parses (no Click error).
3. `endless task add "X" --phase maybe` creates a task at phase=maybe.
4. `endless task list --sort phase` puts maybe items after later items.
5. `endless task update <id> --phase maybe` round-trips through events.
6. After implementation, add the deferred-pruning meta task at phase=maybe to
   demonstrate the new value works end-to-end.
