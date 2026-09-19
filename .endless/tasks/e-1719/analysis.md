# E-1719 execution status & adjudication

## Phase 1 — DONE, landed + assumed. Phase 2 — 177 applied; +10 pending re-run.

The record-only slice (nullable branch, historical `--ts`, explicit-value
`task.landed`) shipped and the 177 confident landings were applied on main.

## CORRECTION: `validate-db` is NOT clean — but it was already dirty pre-E-1719.
My earlier "validate-db clean" claim was wrong; I never ran it against the real
(polluted) ledger. The dirtiness has three PRE-EXISTING causes, none introduced
by this backfill:
1. Task status/vocab drift (`needs_plan`→`unplanned`, `verify`→`unverified`,
   `in_progress`→`underway`) — the ~341 task "mismatches". This is the actual
   comparison noise, unrelated to landings.
2. Sandbox test-tasks leaking into the real ledger (`UNIQUE ... tasks.id` for
   E-1..E-96, foreign node ids) — every `e-NNNN-verify.sh` doing `task add
   --db sandbox` has polluted the real ledger.
3. `task_landings` can't be projected: the projector SKIPS session events
   (projector.go:108) so its temp `sessions` table is empty — every landing
   with a session_id fails the session FK (E-1443, E-1719 itself, hundreds,
   all predating this work). Separately, ~38 of the backfilled tasks predate
   the event ledger (no `task.created` event, e.g. E-448) so their landings
   fail the task_id FK. Both are pre-existing projector limits; the backfill
   just points 38 more landings at limit #2.

Why the live data is safe regardless: validate-db's pass/fail is the TASK
comparison (the `task_landings` lines are projection warnings, not failures);
the live rows are correct (session NULL, branch NULL, historical dates); and
`rebuild-db` only rebuilds tasks/decisions/decision_relations — it NEVER
rebuilds task_landings, so there is no path that drops the backfilled rows.

Follow-ups worth filing (separate from E-1719): projector should replay session
events + materialize pre-ledger tasks (or validate-db should special-case
task_landings); and the sandbox-events-in-real-ledger pollution.

## Confident 177 — applied. Clean set: all ancestors of main, none reverted
(E-1108 excluded; E-1669 included), 0 already recorded. Distinct ids 126 -> 303.

## The 25 review-bucket ids — FINAL disposition
Criterion: a task gets a landing iff its code actually landed on main (a real
feature/merge commit that is an ancestor of main), incl. code later removed.

ADDED to the driver (10, pending a re-run of `--apply` from main):
- Feature commit, still on main: E-1084 7199f5fa, E-1275 5b4413dc,
  E-1182 e1b25174, E-991 0b0f1d00, E-992 0b0f1d00, E-1002 e372759e.
- Landed then later removed (per Mike's rule): E-917/918/921 cf834b0b
  (removed by E-1711), E-1126 c11ba0a0 (partly removed by E-1582).

EXCLUDED — epic-shaped umbrellas, no single landing commit (their feature
commits belong to child tasks already in the 177): E-971, E-995. Both are
pre-epic-era tasks that would today be epics; their children land individually,
so the parent never "lands". (Retyping them to epic is a separate concern.)

OPEN — Mike's call, not yet decided: E-1333 (real commit but docs/guide, not
code) and E-1137 (the task's landed work was itself a revert).

EXCLUDED — plan-only / bare-mention, no feature commit: E-894, E-993, E-1031,
E-1085, E-1113, E-1129, E-1186, E-1272, E-1335, E-1037, E-1509. Plus N/A:
decisions D-958/1007/1094/1175/1199/1361/1373; orphan E-783.

## Driver: tests/tasks/e-1719-backfill.sh (187 pairs = 177 + 10)
Idempotent: dedups against task_landings each run, so re-running `--apply` emits
only not-yet-recorded ids (verified: 10 emit, then 0). Node id = machine node
(no ba15). Run from the MAIN checkout (refuses worktrees).
