# E-1579 — Type-aware status-transition validator (epic/research reject `verify`)

## What shipped

Generalized E-1577's per-type terminal gate into a single table-driven
type→forbidden-status validator in the Python CLI write path
(`src/endless/task_cmd.py`):

- `_TYPE_FORBIDDEN_STATUSES = {"research": ("verify","assumed","confirmed"),
  "epic": (...)}` — the per-type lookup table.
- `_require_status_allowed_for_type(status, task_type)` (renamed from
  `_require_terminal_allowed_for_type`) raises on a forbidden flip.

`research` and `epic` now reject `verify` in addition to E-1577's
`assumed`/`confirmed`. They terminate via `completed` (epics auto-derive per
E-1541; research ends in `completed --outcome` per ED-1502) and never go
through user-testable verification. Universal terminals `obsolete`/`declined`
stay allowed for all types. Both write paths that can set `verify`
(`add_item`, `update_plan`) already call the helper, so no new wiring.

## Decisions resolved during planning (changed the filing)

1. **Python, not a new Go table.** The filing said "per-type lookup table
   alongside the TaskType enum" (Go). But E-1577 already gates the same two
   types in Python, and ED-1506 keeps the Go event executor mechanical with
   status/type policy in the CLI. A Go table would split one concept across two
   languages and force Python to shell out just to validate a status string.
   The unified Python validator ports to Go later as one piece.

2. **`--force` dropped** (title amended). This is a type-correctness invariant,
   not a soft policy: the fix for a rejected flip is to change the task *type*,
   not override the gate. Matches E-1577's hard gate and the "gates, not
   guardrails" rule.

## Tests

- `tests/test_type_status_gate.py` — unit (16 cases): verify rejected for
  research/epic via both paths; verify allowed for task/bug; E-1577
  assumed/confirmed rejection preserved; universal terminals allowed for all.
- `tests/tasks/e-1579-verify.sh` — end-to-end against the sandbox DB, modeled
  on the `e-1577-verify.sh` prototype (the shape E-1596 references). 12 checks,
  ALL PASSED.

## Follow-up (not this task)

E-1543's epic-only super-gate (reject all non-sticky statuses for epics) will
extend the `epic` entry in `_TYPE_FORBIDDEN_STATUSES`.
