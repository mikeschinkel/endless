You are implementing **E-1401 (FIVE-ALARM)** — gate event emission on resolvable session_id. The current Python emit_event path silently drops `session_id` from the actor block when the resolver returns None. Result: events fire with no attribution, downstream materialized tables lose data, the system silently degrades.

Mike: "Inability to determine a valid session ID and to do so accurately and robustness should be treated as a FIVE ALARM FIRE!"

## The bug (concrete)

`src/endless/event_bridge.py:16` `emit_event()` does this:

```python
if session_id is None:
    try:
        from endless.task_cmd import _current_endless_session_id
        eid = _current_endless_session_id()
        if eid is not None:
            session_id = str(eid)
    except Exception:
        session_id = None
```

When `_current_endless_session_id()` returns None (which happens with multiple sibling Claude panes, per E-1294's documented "0 or 2+ falls through to None" behavior), `session_id` stays None, the event emits without it, and the downstream Go handlers see an empty `Actor.SessionID`. The E-1322/E-1381 guard correctly skips, but the cost is silent activity loss.

## The fix

After resolution attempt, if the actor kind warrants session attribution AND `session_id` is still None, **raise `click.ClickException` with an actionable message**. Do NOT emit the event.

Gate logic:

```python
ATTRIBUTION_REQUIRED = {"cli", "hook"}

if actor_kind in ATTRIBUTION_REQUIRED and not session_id:
    raise click.ClickException(
        "Cannot determine the Endless session for this pane. "
        "Run `endless task bind <task-id>` to bind this pane to a task, "
        "or pass session_id explicitly. "
        "Silent attribution loss is not acceptable; refusing to emit this event. "
        "(E-1401)"
    )
```

ActorKind not in the set (e.g., `system`, `web`):
- `system`: cron-style invocations may legitimately have no Claude session attribution; allow empty session_id.
- `web`: web UI users have user_id attribution, not session attribution; allow empty session_id.

## Tests

In `tests/test_event_bridge.py` (or wherever event_bridge has tests; create the file if needed):

1. Mock `_current_endless_session_id` to return None, call emit_event with `actor_kind="cli"`. Assert `click.ClickException` raised.
2. Same with `actor_kind="hook"`. Assert raised.
3. Same with `actor_kind="system"`. Assert event emits (no exception, no session_id in actor).
4. Same with `actor_kind="web"`. Assert event emits.
5. With resolver returning a real id, `actor_kind="cli"`. Assert event emits with session_id populated.
6. With explicit session_id argument (bypassing resolver), `actor_kind="cli"`, resolver returns None. Assert event emits with the explicit session_id (the gate is only on resolver-derived None).

## E2E verification (critical — Mike was burned by E-1322 landing without e2e)

```
# In a pane with multiple sibling Claude sessions (today's failure mode):
endless task add "probe E-1401 gate"
# EXPECTED: click.ClickException with the actionable message; non-zero exit.

# Bind the pane:
endless task bind <some-task-id>
# Then retry:
endless task add "probe E-1401 after bind"
# EXPECTED: success; new task created.
endless sql "SELECT * FROM session_tasks WHERE task_id = <new-id>"
# EXPECTED: one row.
```

If you can't reproduce the "multiple sibling Claude sessions" condition in your worktree, run the test logic against a mocked resolver and ALSO state in the handoff message what manual e2e you couldn't run.

## Pairing with E-1402

E-1402 strengthens the resolver to handle multi-sibling cases deterministically (pick most-recently-active sibling instead of giving up). It pairs with this task: with E-1402 in place, the gate (E-1401) fires only when no defensible session choice exists.

If E-1402 ships FIRST, the resolver returns more often and the gate fires less. Order is good but not required — they're independently valuable. Coordinate via the `relates_to` link.

## Files to touch

- `src/endless/event_bridge.py` — add the gate after the resolver call
- `tests/test_event_bridge.py` (new or extend existing) — the six test cases above
- Possibly `docs/` — document the gate behavior so callers know what to do when it fires

## House rules

- The actionable message MUST tell the user EXACTLY what to do (`endless task bind <task-id>`). Don't make them grep the codebase.
- Don't add a `--force` or `--no-session` bypass flag. Per `feedback_gates_not_guardrails`: bypasses get taken. If a caller genuinely needs an unattributed event, they pass `actor_kind="system"`.
- Per LESSONS.md "Session-ID attribution failures are a FIVE-ALARM fire" (2026-05-17): any session-attribution-dependent code path treats resolver failure as an error, never silent degradation.

## Closing

- `endless task update E-1401 --status verify --outcome "emit_event refuses to emit when actor_kind in {cli, hook} and session_id unresolvable; actionable error message; system/web actors unaffected; tests cover all paths; e2e verified or limitation called out."`
- Handoff: "To verify: from an unbound pane, `endless task add 'test'` errors out cleanly; `endless task bind <id>` then retry succeeds."
- Do NOT auto-land. Ask Mike.
