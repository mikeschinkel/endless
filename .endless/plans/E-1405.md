# E-1405: Add `urgent` phase above `now`

## Context

The current phase set is `now > next > later > maybe` (4 values; `blocked` is a *status*, not a phase, despite what E-1405's description says). There is no slot above `now` for time-critical work. E-1401 already invented an ad-hoc `FIVE-ALARM` marker in its title — concrete evidence the gap is real.

Goal: introduce `urgent` as the new highest phase. Final ordering: **`urgent` > `now` > `next` > `later` > `maybe`**. Default phase on `task add` stays `now`. After the phase lands, retroactively promote E-1401, E-1402, E-1404, and E-1405 itself to `urgent`.

Decisions confirmed with user:
- Phase set becomes `{urgent, now, next, later, maybe}`. `blocked` stays a status only.
- Web UI templ changes (badge styling for `urgent`) are **deferred** to a follow-up task. Sort order in the Go web queries IS updated here, so the web list ranks urgent tasks correctly even before the badge lands.
- The stale `"blocked": "blocked"` mapping in `_phase_for_heading` is fixed as part of this task (it currently emits an invalid phase that the events layer rejects).

## Out of scope

- Web UI badge styling (color/variant for `urgent`) — separate follow-up.
- DB CHECK constraint — schema has no CHECK on `phase` today; validation is enforced at the events boundary (Go `ValidatePhase`) and CLI boundary (Python `click.Choice`). Not adding one here.
- Status changes / `blocked` rework.

## Files to modify

### Go: phase enum (the canonical gate)

- `internal/events/phase.go:8-13` — add `"urgent": true` to `ValidPhases`. Update error message at line 19 to list `urgent` first: `"valid: urgent, now, next, later, maybe"`.
- `internal/events/phase_test.go:11` — add `"urgent"` to accepted list.
- `internal/events/phase_test.go:31-40` — update `want` map and length assertion (now 5 keys).
- `internal/events/phase_test.go:19` — `"blocked"` stays in the *rejected* list (still not a phase).

### Go: web sort ordering

- `internal/web/queries.go:180-186` — extend CASE to put `urgent` at 0; renumber: `urgent=0, now=1, next=2, later=3, maybe=4, else=5`.

### Python: CLI `--phase` validation (5 sites)

Add `"urgent"` to the head of each `click.Choice` and update the help string where applicable. Order in the Choice list signals priority to humans reading `--help`.

- `src/endless/cli.py:748-750` (`task list`) — `click.Choice(["urgent", "now", "next", "later", "maybe"])`.
- `src/endless/cli.py:824-826` (`task next`) — same.
- `src/endless/cli.py:888-890` (`task active`) — same.
- `src/endless/cli.py:921-923` (`task add`) — same; **default stays `"now"`**; help text `"Phase: urgent, now, next, later, maybe (default: now)"`.
- `src/endless/cli.py:1001-1003` (`task update`) — same; help text `"Phase: urgent, now, next, later, maybe"`.

### Python: sort CASE statements

- `src/endless/task_cmd.py:969` (`task list` sort) — extend CASE: `WHEN 'urgent' THEN 0 WHEN 'now' THEN 1 WHEN 'next' THEN 2 WHEN 'later' THEN 3 WHEN 'maybe' THEN 4 ELSE 5`.
- `src/endless/task_cmd.py:1147-1149` (`active_tasks`) — same renumbering.

### Python: markdown heading → phase mapping

- `src/endless/task_cmd.py:520-535` (`_phase_for_heading`) —
  - Add: `"urgent": "urgent"`, `"asap": "urgent"`, `"five-alarm": "urgent"` (synonyms covering common heading patterns).
  - **Remove** `"blocked": "blocked"` (line 535) — it produces an invalid phase that downstream validation rejects. `blocked` is a status, not a phase; a "Blocked" heading should fall through to default rather than fabricate an invalid value.
  - Order matters: insert `urgent` aliases ahead of the `now`-family keys so a heading like "Urgent now" maps to `urgent`, not `now`.

### Python: tests

- `tests/test_cli.py:215-225` — `test_task_add_rejects_invalid_phase_at_parse_time`: extend the iteration to include `"urgent"` in the must-appear-in-output list.
- Add a new test `test_task_add_accepts_urgent_phase` that runs `task add --phase urgent` and asserts exit_code 0 and that the created task has phase `urgent` (mirror existing `task add` tests).

### Documentation (guide)

- `docs/guide/index.md:55-62` — extend the phase table with a row for `urgent` (placed as the first row in the table so the order reads top-to-bottom): `\`urgent\` | Time-critical priority; takes precedence over \`now\`.`
- `docs/guide/tasks.md` — no example changes required, but if any "valid phases" enumeration exists nearby, append `urgent`. (Verify during implementation; agent inventory found no other enumerations.)
- `README.md:125, 133` — examples mention `now|next|later`; update to `urgent|now|next|later` if those lines enumerate the set. Quick read during implementation will confirm.

### Schema

- `internal/schema/schema.sql:82` — no change. `DEFAULT 'now'` is correct; the column has no CHECK and validation lives at the events boundary.

## Retroactive promotion (after build/install lands)

Once the phase is valid, run:

```sh
endless task update E-1401 --phase urgent
endless task update E-1402 --phase urgent
endless task update E-1404 --phase urgent
endless task update E-1405 --phase urgent   # self
```

## Follow-up tasks to file

1. Web UI `urgent` badge styling — `internal/web/pages/*.templ` currently hide the phase badge when `phase != "now"`. For `urgent`, want a visually distinct badge (likely red/orange) so urgent tasks are eye-catching. File with `--cleans-up E-1405`.
2. (Optional, if uncovered during implementation) any tier/phase combined display logic that hardcodes the 4-phase list and needs `urgent`.

## Verification

1. `just build` succeeds (Go binaries rebuild; templ regen is no-op since templ files are unchanged).
2. `just install` from main checkout — refreshes the editable Python install and `/usr/local/bin/` symlinks. (Note: this plan is being executed in a worktree per workflow; **per the user's locked feedback, do not run `just install` from a worktree** — install from main checkout after merge, or test via `bin-sandbox/` wrappers inside the worktree.)
3. Go tests: `go test ./internal/events/...` — `TestValidatePhase_AcceptsKnownValues` passes for all 5 values; `TestValidatePhase_RejectsUnknown` still rejects `"blocked"`; `TestValidPhases_HasExactlyExpectedKeys` passes with length 5.
4. Python tests: `just test` — `test_task_add_rejects_invalid_phase_at_parse_time` mentions `urgent` in error output; new `urgent`-acceptance test passes.
5. Manual end-to-end inside the worktree's `bin-sandbox/`:
   ```sh
   ./bin-sandbox/endless task add "Test urgent" --phase urgent       # succeeds, prints task ID
   ./bin-sandbox/endless task list --phase urgent --llm              # shows the new task
   ./bin-sandbox/endless task list --sort phase --llm                # urgent rows appear first
   ./bin-sandbox/endless task next --llm                             # urgent task ranks above 'now' tasks
   ./bin-sandbox/endless task add "Bad phase" --phase blocked        # fails with click error listing valid phases including urgent
   ./bin-sandbox/endless guide                                       # phase table now shows urgent
   ```
6. Retroactive promotions (E-1401, E-1402, E-1404, E-1405) succeed against the real DB after merge.
7. Set E-1405 to `verify` with explicit how-to-test in the handoff message; tell the user: "verify by running steps 3–5 above, then promote the four tasks via the commands in the Retroactive promotion section."
