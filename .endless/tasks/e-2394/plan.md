# Plan — `endless session end`

Decided with Mike, 2026-10-17 (E-2300 /needs-plan, Q6 = B, Q7 = C).

## Command

`endless session end <ref>`, ref resolved as `endless session resume` resolves it (E-NNNN, ES-NNNN, bare integer, Claude UUID prefix). Python CLI in `src/endless/session_cmd.py` / `src/endless/cli.py`; the write is a new `endless-go session-query end-session --id N`, which does what the SessionEnd hook does: release the session's worktree lock, then `monitor.EndSession` by id. (Changed during implementation, with Mike, 2026-10-18: the plan first named the `session.ended` event, but that kind has no executor, and session rows are machine-local and never ledgered, so there was never a reason to route this through the ledger.) No SQLite added to Python: liveness comes from Go's `session-liveness`, and last activity from `resume-target`, which gains a `last_activity` field.

Guards, in order:
1. Refuse a session that reads `live`: it is running in a pane Endless can see.
2. Refuse a session whose `last_activity` is within the last 10 minutes unless `--force`: a running Claude fires hooks, so recent activity is evidence it is alive even when its server cannot be reached.
3. Show the session's task, last activity and liveness and ask y/N; `--yes` skips the prompt. With no terminal to prompt on and no `--yes`, refuse.
4. An already-ended session is reported as already ended, exit zero.

After ending, print:

> Ended ES-N (E-M). Its transcript is kept. To get it back, run `endless session resume ES-N`. Until then, E-M can be claimed by another session.

(`session resume` already resumes ended sessions.)

## Claim refusal

E-2300 makes the claim/spawn refusal (`_check_task_ownership` in `src/endless/task_cmd.py`) say, for an `unknown` blocking session, that its server cannot be reached and name `endless start` and `endless session resume ES-N`. Add `endless session end ES-N` as the last route.

## Docs

`docs/guide/sessions.md`: the command, its guards, and the undo route.

## Tests

Python: refuses live; refuses recent activity, `--force` overrides; prompt y and N; `--yes`; no tty without `--yes` refuses; already ended; the printed recovery line; the refusal names `session end` for an `unknown` owner and not for a `live` one.
