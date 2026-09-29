# Replace the false "Plan file synced" hook message with a nudge

## Change

In `handlePostToolUse` (`internal/hookcmd/claude.go`), keep the `isPlanFile` gate and its matcher exactly as they are. Replace the injected text and the `monitor.GetActiveTasks` count it uses:

1. Resolve the session's bound task with `monitor.GetActiveSession(payload.SessionID)` (the same call the write gate uses), reading `sessions.task_id`.
2. Inject (via `writeContextInjection`, same event name):

   ```
   Plan written to <path>. Endless does not capture plan files — attach it to your task with:
     endless task update E-<id> --plan-file <path>
   ```

   `<path>` is `input.FilePath` verbatim. If the session is unbound, the lookup fails, or `task_id` is null/0, print the literal `<id>` in place of `E-<id>`. A failed lookup is not an error: never return `dbReadFailed` from this branch, and never block.
3. Delete the `GetActiveTasks` call and the auto-import NOTE comment (the deleted feature's tombstone); leave one line of comment saying the message is a nudge because Endless does not capture plan files.

## Tests (`internal/hookcmd`, alongside the existing PostToolUse tests)

- Write to a `.claude/plans/x.md` path from a session bound to task 123 → output contains `Plan written to` and `endless task update E-123 --plan-file <that path>`, and does not contain `synced`.
- Same from an unbound session → contains `--plan-file` and the literal `<id>`.
- Write to a non-plan path → no injection (unchanged behavior).

## Out of scope

- Widening `isPlanFile` (E-1098, obsolete).
- Editing `docs/research-2026-09-17-refusal-inventory.tsv` — it is a dated research snapshot.

## Verify

`just test-go` green. Then, in a claimed session, Write any `plan.md` and see the nudge with that session's task id.
