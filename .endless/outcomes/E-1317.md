Added --pane override to `endless session activity` (cli.py + session_activity.py); _resolve_session_id temporarily swaps $TMUX_PANE during the resolver call. Tmux menu item now invokes `endless session activity --pane=#{pane_id} | less`, so tmux substitutes the focused pane at menu-invocation time and the popup's own pane id doesn't break resolution.

Verified end-to-end by simulating popup context (TMUX_PANE=%999 + --pane=%113) — report still resolves to session ES-356 and renders correctly.

357 tests pass.

Land sequence: `endless worktree land E-1317`, `just install`, `endless tmux apply`. Then prefix+e → `a` in any pane should open the report instead of the resolution error.