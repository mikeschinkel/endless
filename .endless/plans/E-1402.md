You are implementing **E-1402 (FIVE-ALARM pair)** — strengthen the session resolver so it picks a session deterministically in the multi-sibling pane case, instead of giving up and returning None. Pairs with E-1401 (refuse-to-emit gate); both are required for the FIVE-ALARM fix to be complete.

## The bug (concrete)

`src/endless/task_cmd.py:1971` `_current_endless_session_id()` calls `_find_sibling_claude_session()` (line 2012) which returns:

- `(None, 0)` — no sibling Claude session
- `(eid, 1)` — exactly one match → use it
- `(None, n)` — n>1 matches → ambiguous, return None

Today's failure: with 5+ active Claude sessions in sibling panes, the n>1 branch fires, the resolver returns None, and the downstream emit_event silently emits attribution-less events (until E-1401 lands). Mike's session ran for an entire conversation losing attribution because of this branch.

## The fix

When n>1 sibling matches exist, pick the most-recently-active session instead of giving up. Concretely:

```python
def _find_sibling_claude_session() -> tuple[int | None, int]:
    # ... existing setup unchanged ...
    if not matches:
        return None, 0
    if len(matches) == 1:
        return matches[0]["endless_session_id"], 1
    # E-1402: multi-sibling case. Pick most-recently-active.
    # Query sessions.last_activity for the candidate eids; sort DESC; pick first.
    candidate_eids = [m["endless_session_id"] for m in matches]
    from endless import db
    placeholders = ",".join("?" * len(candidate_eids))
    rows = db.query(
        f"SELECT id FROM sessions "
        f"WHERE id IN ({placeholders}) AND state != 'ended' "
        f"ORDER BY last_activity DESC, id DESC LIMIT 1",
        tuple(candidate_eids),
    )
    if rows:
        return rows[0]["id"], 1
    # Defense in depth: if the DB lookup returns nothing (e.g., all ended),
    # behave as the old contract did.
    return None, len(matches)
```

**Tie-breaker: `id DESC`.** When two sessions have the same `last_activity` (rare but possible at second precision), pick the higher id (more recently created). Deterministic, no flakiness.

**Filter `state != 'ended'`.** A sibling with state=ended shouldn't be picked — those are dead sessions that happen to have a companion file lingering.

**Document the precedence rule** in the docstring of both `_find_sibling_claude_session` and `_current_endless_session_id`. The old docstring's "0 or 2+ falls through to None" is now wrong for the 2+ case.

## Caller compatibility

`_find_sibling_claude_session` has two callers per the docstring:
- `_current_endless_session_id` (the resolver entry point) — wants n=1 semantics, now gets them deterministically more often.
- `claim_item` / `bind_item` (per the docstring) — these want to *refuse* on ambiguity, with a policy refusal pointing at E-1244.

**You must NOT break the policy-refusal callers.** Two options:

A. Add an optional parameter `strict_unique=False`. When `True`, preserves the old behavior (return `(None, n)` on ambiguity). `claim_item` / `bind_item` pass `strict_unique=True`. Resolver entry points use the default `False` and get the new deterministic pick.

B. Split into two functions: `_find_sibling_claude_session_strict` (old behavior, used by claim/bind) and `_find_sibling_claude_session_pick` (new behavior, used by resolver).

Pick whichever fits the codebase better. Option A is one signature, two callers. Option B is two functions, clearer at call sites.

## Tests

In `tests/test_task_cmd.py` (or wherever session-resolver tests live):

1. 0 matches → `(None, 0)`. (Existing behavior preserved.)
2. 1 match → `(eid, 1)`. (Existing behavior preserved.)
3. 2+ matches with distinct last_activity → returns the most recent eid as `(eid, 1)`. (New behavior.)
4. 2+ matches with identical last_activity → returns the higher-id eid. (Tie-breaker.)
5. 2+ matches but all with `state='ended'` → returns `(None, n)`. (Filter applied.)
6. Strict mode (whichever knob you implement) → preserves old `(None, n)` semantics. (Policy-refusal callers.)

## E2E verification

The real-world scenario: from a Claude pane with multiple sibling Claude panes alive.

```
# Set up: ensure at least 2 sibling Claude sessions are alive (e.g., in worktree tmux windows).
endless sql "SELECT id, state, last_activity, process FROM sessions WHERE state != 'ended' ORDER BY last_activity DESC LIMIT 5"

# Run the resolver indirectly via emit_event:
endless task add "probe E-1402 resolver"

# Check the emitted event:
tail -1 .endless/db-ledger/$(ls -t .endless/db-ledger/ | head -1)
# EXPECTED: actor.session_id populated with the highest-last_activity sibling's id
```

Without E-1401 in place, this is the only way to verify. If E-1401 has landed and gates emission, the test setup needs to ensure the resolver succeeds (otherwise the gate fires before you can observe success).

## Pairing with E-1401

E-1401 errors when the resolver returns None. E-1402 reduces how often that happens by making n>1 cases resolve. Both ship for the FIVE-ALARM fix to be complete:
- E-1402 alone: silent degradation in the truly impossible cases (0 sibling, all ended).
- E-1401 alone: gate fires correctly but is annoying because the multi-sibling case is common.
- Both: gate fires only when no defensible choice exists.

## Files to touch

- `src/endless/task_cmd.py` — modify `_find_sibling_claude_session` per the fix above; update both docstrings; potentially adjust `_current_endless_session_id` if Option B chosen.
- Wherever `claim_item` and `bind_item` live (likely `task_cmd.py` too) — if Option A, pass `strict_unique=True`.
- `tests/test_task_cmd.py` (or similar) — add the six test cases.

## House rules

- Deterministic precedence required. No randomness, no "pick any." `last_activity DESC, id DESC` is the contract.
- Document the precedence rule visibly. Future readers need to know why "the most recent" is the answer when picking among ambiguous siblings.
- Per LESSONS.md "Session-ID attribution failures are a FIVE-ALARM fire": resolver failure is acceptable only when no defensible choice exists, and even then the gate (E-1401) catches it.

## Closing

- `endless task update E-1402 --status verify --outcome "resolver picks most-recently-active sibling in multi-match case (last_activity DESC, id DESC tie-break); state=ended filtered out; strict-mode preserved for claim/bind callers; tests cover all branches."`
- Handoff: "To verify: with multiple sibling Claude sessions alive, `endless task add 'test'` now emits an event with a populated actor.session_id."
- Do NOT auto-land. Ask Mike.
