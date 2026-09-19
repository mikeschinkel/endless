# Plan: make `--keep-status` hold the status across every auto-transition

## Decision

Implement option (a) from the analysis: when `--keep-status` is passed, the
Python layer sends the task's current status explicitly, so the executor's
existing "caller wins" precedence suppresses the plan-attach promotion. No
change to the event vocabulary, and nothing that collides with E-1671.

If review prefers option (b) — a `KeepStatus` field on
`TaskFieldsUpdatedPayload` — say so before implementing; the rest of this plan
holds either way.

## Scope of the contract change

`--keep-status` today is documented as the done-task auto-revisit escape hatch.
It becomes: **no auto-transition fires; the status you see is the status you
keep.** That covers all three:

1. E-1762 auto-revisit (done task, plan text edited) — already guarded
2. E-1845 auto-untriage (pre-work task, description edited) — already guarded
3. plan-attach promotion (`untriaged`/`unplanned` + non-empty `--text` ->
   `submitted`) — the gap this task closes

An explicit `--status` in the same call still wins over everything; passing both
`--status` and `--keep-status` should be rejected as contradictory rather than
silently resolved.

## Implementation

1. `src/endless/task_cmd.py` (`update_task`, near the `auto_revisit` /
   `auto_untriage` block around lines 3990-4045): when `keep_status` is set and
   `status is None`, add the row's current status to the fields map. Placing it
   with the other status resolution keeps the three transitions readable as one
   decision rather than two-here-one-in-Go.
2. Reject `--status` together with `--keep-status` in `src/endless/cli.py`
   (or in `update_task`, wherever the other argument conflicts are caught).
3. Correct the help text in `src/endless/cli.py:1718` — drop "on a done task",
   state that no auto-transition fires.
4. Update `endless guide tasks`, which frames the flag as the escape hatch for a
   "typo- or formatting-only edit". Same wording appears in `CLAUDE.md` and the
   status-lifecycle prose; re-sync whichever copies the guide test asserts.

## Verification

`tests/tasks/e-NNNN-verify.sh`, self-contained against a throwaway
XDG_CONFIG_HOME and a temp project (shape per `tests/tasks/e-1880-verify.sh`):

- **The regression itself.** Create a task, leave it `unplanned`, update it with
  `--text-file` + `--keep-status`, assert status is still `unplanned` and the
  text did change. Without `--keep-status` the same update must still promote to
  `submitted` — the promotion is correct default behavior and must not be lost.
- Same pair from `untriaged`.
- E-1762 still guarded: a done task (`assumed`) + text edit + `--keep-status`
  stays `assumed`; without the flag it goes `revisit`.
- E-1845 still guarded: a `ready` task + description edit + `--keep-status`
  stays `ready`; without the flag it resets.
- `--status` and `--keep-status` together exits non-zero.
- Fold-in regression: `uv run pytest tests/` for the task-update tests, and
  `go test ./internal/events/...` (the promotion's white-box test lives in
  `internal/events/pre_judgment_test.go` and must stay green — the executor
  behavior is unchanged under option (a), which is itself worth asserting).

## Out of scope

Any change to `internal/events` payloads or the executor. Under option (a) the
Go side is untouched, which is the point.
