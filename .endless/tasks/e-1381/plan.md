You are implementing **E-1381** — fix the auto-capture guard in `session_tasks` so it actually records rows from real `endless task add` / `endless task update` / `endless task claim` etc. invocations. E-1322 landed (commit `4652b1a` on main) but its guard is incoherent with the actual actor model; no rows have ever been produced in production. The fix is one line of code plus updating tests that asserted the buggy behavior.

## The bug

`internal/events/session_tasks.go` defines:

```go
func shouldRecordSessionTouch(evt *Event) bool {
    return evt.Actor.Kind == ActorSession && evt.Actor.SessionID != ""
}
```

But `Actor.Kind` describes event ORIGIN (`cli` / `hook` / `web` / `system`) per its docstring in `internal/events/event.go`, not author. The ledger evidence:

```
$ grep -ho '"actor":{"kind":"[a-z]*"' .endless/db-ledger/*.jsonl | sort | uniq -c
1518 "actor":{"kind":"cli"
```

Zero events ever emitted with `kind=session`. Every real `endless task add` carries `kind=cli` with `session_id` populated from TMUX_PANE resolution. The current guard rejects all of them.

## The fix

In `internal/events/session_tasks.go`, change the guard to:

```go
func shouldRecordSessionTouch(evt *Event) bool {
    return evt.Actor.SessionID != ""
}
```

Drop the `ActorKind` check entirely. SessionID alone is the criterion: if the event carries session attribution, record the touch; if not, skip.

Update the docstring on `shouldRecordSessionTouch` to reflect the corrected semantics. The previous comment ("ActorKind == ActorSession only, even though CLI/hook actors may carry a session_id") is wrong and should be removed.

## Tests to fix

In `internal/events/session_tasks_test.go`:

- **`TestSessionTasks_NoRowForCLIActor`** asserts the buggy behavior (CLI actor with session_id produces no row). Either:
  - Rename it to `TestSessionTasks_CLIActorWithSessionRecordsRow` and assert the opposite (one row, populated from SessionID), OR
  - Delete it and add a new positive case for CLI-actor-with-session.
  - Add another negative case: CLI actor with EMPTY session_id should NOT produce a row. The "guard rejects when SessionID is empty" path still needs coverage.

- **`TestSessionTasks_NoRowForEmptySessionID`** stays — empty SessionID still skips the upsert.

- The existing positive tests (`TestSessionTasks_CreatedBySession`, etc.) probably use a hand-built `ActorSession` kind actor. Adjust them to use `kind=cli` with a `session_id` set, since that's the realistic shape. The test should mirror production.

## End-to-end verification

Beyond unit tests, prove the loop actually closes:

1. `just build` cleanly.
2. `just test` passes.
3. From a fresh tmux pane bound to a session (or from this Claude session via Bash):
   ```
   XDG_CONFIG_HOME=$HOME/.config endless task add "probe E-1381"
   XDG_CONFIG_HOME=$HOME/.config endless sql "SELECT * FROM session_tasks WHERE task_id = <returned-id>"
   ```
   Expected: one row, created_at == updated_at.
4. Update the same task:
   ```
   XDG_CONFIG_HOME=$HOME/.config endless task update <id> --description "x"
   XDG_CONFIG_HOME=$HOME/.config endless sql "SELECT * FROM session_tasks WHERE task_id = <id>"
   ```
   Expected: same row, updated_at advances, created_at unchanged.
5. Spot-check the ledger that the events have the expected shape (`actor.kind=cli`, `actor.session_id=<n>`).

Don't declare done until you've personally run the synthetic e2e — that's the gap that let E-1322 land broken.

## Files to touch

- `internal/events/session_tasks.go` — one line of code, plus docstring fix.
- `internal/events/session_tasks_test.go` — invert the CLI-actor test, adjust positive tests to use the realistic CLI+session shape, keep coverage for the empty-SessionID guard.

## House rules

- Build with `just build`, not bare `go build`. Binaries go to `./bin/`.
- `just install` only from the main checkout, never from this worktree.
- Inside this worktree, `endless ...` uses the sandbox DB; for real-DB queries during e2e verification, prefix with `XDG_CONFIG_HOME=$HOME/.config`.
- Sandbox should auto-provision when spawn runs; verify with `endless task detail E-1381` showing in_progress + claimed.

## Closing

- `endless task update E-1381 --status verify --outcome "guard now keys on SessionID alone; e2e verified that endless task add produces a session_tasks row; broken test inverted"`. Status is `verify`, **not** `completed` or `assumed` — Mike confirms.
- Include in your handoff: "To verify: `endless task add 'throwaway'`, then `endless sql 'SELECT * FROM session_tasks WHERE task_id=<returned>'` shows one row."
- Do NOT auto-land. Ask Mike before `endless worktree land`. Mike's standing rule.

## Context links

- E-1322 (`internal/events/session_tasks.go`, commit `4652b1a` on main) — the landed implementation this fix corrects.
- E-1352 (decision record) — events-as-primitive posture this work serves.
- LESSONS.md entry "Verify the actor model before specifying actor-based guards" (2026-05-16) — root cause analysis.
