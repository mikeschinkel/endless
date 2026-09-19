# Validate ENDLESS_SESSION_ID against the sessions table before trusting it

The env-var layer of `_current_endless_session_id` (`src/endless/task_cmd.py`) has two
independent defects, both reachable via `ENDLESS_SESSION_ID`. Both are fixed here.

## Problem 1 — no validation against the sessions table

It accepts any digit string in `ENDLESS_SESSION_ID` and returns it as the current session id without checking that a matching row exists in the `sessions` table:

```python
env_id = os.environ.get("ENDLESS_SESSION_ID")
if env_id and env_id.isdigit():
    return int(env_id)
```

A bogus or stale value (`ENDLESS_SESSION_ID=999999`, or a real id from a since-ended session) passes the E-1401 attribution gate in `event_bridge.py` — the gate only checks that `session_id` is non-empty, not that it's valid — and every event then gets attributed to a non-existent or wrong session. Silent misattribution.

## Problem 2 — only the integer PK is accepted; a UUID is silently ignored (folded from E-1470)

The same `env_id.isdigit()` guard accepts ONLY the integer PK. A Claude session UUID (e.g. `ea2f7409-fd55-48a2-b1a7-9670b9ef9471`) is not all-digits, so it fails the guard and falls through with no signal — even though `sessions.session_id` is UNIQUE and the UUID is the id form a user (or `$CLAUDE_CODE_SESSION_ID`) is most likely to have on hand. Worse, the E-1401 gate's own remediation message ("Export ENDLESS_SESSION_ID=...") implies any id form works, so a user who exports their UUID gets silent non-resolution with no hint why. Surfaced during E-1470's resolver investigation and folded here per Mike (2026-05-25).

## Origin

This was originally part of E-1415 ("Validate resolved session id against DB in `_current_endless_session_id`"). E-1415 was set to `obsolete`: its companion-file-validation portion is subsumed by E-1426 (which retires companion files — once the resolver reads `sessions.process` directly, a row read from the DB inherently exists). The **env-var layer is independent of companion files** and that concern survives E-1415's obsoletion — hence this dedicated task. Problem 2 is a separate defect in the same guard, split out of E-1470.

## Fix

In `_current_endless_session_id`, replace the bare `isdigit()` acceptance with a lookup that accepts EITHER id form and confirms the row exists:
- Integer PK: look up the id in `sessions`.
- UUID: look up `sessions.session_id` (UNIQUE) and resolve to its integer PK.

If the value matches no row (either form):
- Treat the env-var layer as not-resolved (fall through to the next resolution layer).
- Write a one-line warning to stderr naming the bad value: e.g. `warning: ENDLESS_SESSION_ID=999999 does not match any session; ignoring.`

Do not silently accept it, and do not silently discard it without telling the user — a stale env var in their shell is something they need to know about and fix.

Also align the E-1401 gate's remediation message with what's actually accepted (integer PK or UUID), so the suggested `Export ENDLESS_SESSION_ID=...` is not misleading.

## Coordination

- **E-1426** (urgent, in progress) rewrites `_current_endless_session_id`'s companion/process layer. This task touches the same function's *env-var* layer (layer 1, which runs before the process lookup). Whoever holds E-1426 should be aware this validation is landing in the same function; sequence to avoid a merge collision.
- **E-1401** is the attribution gate this validation protects. The gate itself stays a presence check; this task makes the resolver stop feeding it invalid ids, and fixes the gate's misleading remediation-message wording (Problem 2).

## Verification

1. `ENDLESS_SESSION_ID=999999 endless task add "x"` from a context with no other resolution layer available → resolver ignores the bogus value, stderr warns, and the E-1401 gate refuses (no misattribution).
2. `ENDLESS_SESSION_ID=<valid live integer id>` → resolver returns it, no warning.
3. `ENDLESS_SESSION_ID=<valid live session UUID>` → resolver resolves it to the integer PK, no warning (Problem 2).
4. `ENDLESS_SESSION_ID=<id of an ended session>` → treated as invalid (decide during planning whether "ended" counts as invalid, or only "no row at all"; present the choice to Mike).
5. Tests covering valid-integer / valid-UUID / non-existent / malformed env-var values.
