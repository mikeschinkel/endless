Extended `_current_endless_session_id()` (task_cmd.py:1968) with a third layer: single-sibling-pane fallback. Now every caller transparently gets the right session when the user is in a shell pane next to a Claude pane in the same tmux window.

Cleanup: `emit_event` (event_bridge.py) simplified — the inline 3-layer fallback from E-1287 became redundant once the resolver handles it. Back to a single resolver call.

No changes needed in claim_item / bind_item: their explicit `_find_sibling_claude_session` blocks still run only when the resolver returned None (0 or 2+ match cases), which is exactly when their refusal policies apply.

release_item gets the fix transparently — running `endless task release` from a shell pane now resolves to the sibling Claude session.

326 tests pass.

Verification deferred to land+install. From a shell pane in a window with a single Claude pane, `endless task add 'X'` should now produce a ledger entry with `actor.session_id` populated to the sibling Claude session id.