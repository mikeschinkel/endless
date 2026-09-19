You are implementing **E-1307** — add a new subcommand `endless session id` that prints the current session's integer id to stdout for shell-substitution use.

## Why

E-1401 (just landed) added a gate in `emit_event` that refuses to fire when the resolver can't determine the session for a cli/hook actor. The gate's error message tells the user to do one of:

1. Run from a Claude pane.
2. Export `ENDLESS_SESSION_ID=<id>` (find ids via `endless session show`).
3. Export `ENDLESS_SESSION_ID="$(endless session id)"`.
4. Run `endless task bind <task-id>`.

Option 3 already references this command. Today it errors with "No such command 'id'". Shipping E-1307 makes option 3 live and lets us drop the verbose option 2.

## What to build

A new Click subcommand `endless session id` that:

- Takes no required arguments.
- Resolves the current session using the **same 3-layer logic** as `_current_endless_session_id` in `src/endless/task_cmd.py:1971` (env → pane-direct → single-sibling).
- On success: prints the integer EID on stdout, followed by a newline. **Nothing else on stdout** — no labels, no decoration. The output must be safe to interpolate as `ENDLESS_SESSION_ID="$(endless session id)"`.
- On failure (no session resolves): prints a one-line diagnostic to **stderr** explaining which layers were tried and exits non-zero.

**Reuse the existing resolver — don't reimplement it.** The cleanest path: import `_current_endless_session_id` from `task_cmd.py` (cross-module imports of leading-underscore functions are an established pattern in this codebase — `session_cmd.py` already calls into `task_cmd.py` and vice versa).

If the resolver returns None, you do need diagnostic detail for stderr — call `_find_sibling_claude_session` yourself to differentiate "no tmux" / "0 siblings" / "2+ siblings" / "no env var, no companion" cases, and tailor the stderr message accordingly. The user should know whether their problem is "not in tmux", "ambiguous siblings, claim from the right pane", or "no Claude session anywhere nearby".

## Files

- `src/endless/cli.py` — register the subcommand under `session_cmd` (follow the `cd` pattern at lines 610-629).
- `src/endless/session_cmd.py` — add the implementation function (e.g. `session_id_resolve()`); the `cd` resolver at line 1245 is the analogous reference.
- `src/endless/event_bridge.py` — after the new command is shipped, **delete the "Export ENDLESS_SESSION_ID=<id>; see `endless session show` for live session ids" hint** from the gate error message and remove the `# NOTE: once `endless session id` ships ...` comment block above it. The remaining options should become: (1) run from Claude pane, (2) the `$(endless session id)` form, (3) `endless task bind`.
- `tests/test_session_id.py` (new) — tests covering each resolution layer plus the failure path. Use the `no_session_stub` marker to opt out of conftest's autouse resolver stub (per E-1401).

## Test cases

1. `ENDLESS_SESSION_ID=42` env var → stdout is exactly `42\n`, exit 0.
2. `TMUX_PANE` matches a companion file → stdout is that companion's EID, exit 0.
3. No env, no TMUX_PANE → resolver fails → stderr names the cause, exit non-zero.
4. Exactly one sibling Claude pane → returns that EID.
5. Multiple sibling Claude panes → exit non-zero with stderr saying "ambiguous; claim from a specific pane".
6. The CLI invocation: `runner.invoke(main, ["session", "id"])` against each scenario above.

## Don'ts

- Don't print the UUID. ENDLESS_SESSION_ID must be the integer (the resolver in `_current_endless_session_id` only accepts digit strings).
- Don't print anything decorative on stdout. Bare integer + newline. Logs go to stderr if at all.
- Don't add `--format` / `--json` flags. One job, one output.
- Don't extend or modify `_current_endless_session_id` itself — its current behavior is load-bearing for E-1401's gate.

## E2E verification

From a Claude pane (where the resolver works):

```sh
endless session id
# Expect: bare integer, exit 0.

ENDLESS_SESSION_ID="$(endless session id)" endless task add "probe E-1307"
# Expect: task created, no E-1401 gate error.
```

From an unbound shell pane:

```sh
env -u ENDLESS_SESSION_ID -u TMUX_PANE endless session id
# Expect: stderr diagnostic, non-zero exit, no stdout output.

env -u ENDLESS_SESSION_ID -u TMUX_PANE -- bash -c 'echo "[$(endless session id 2>/dev/null)]"'
# Expect: prints "[]" — confirms nothing leaks to stdout on failure.
```

## Known adjacent bug — do NOT fix it here

E-1408 tracks a separate bug: SessionStart hook captures empty `TMUX_PANE` silently, leaving the companion file without a `pane_id` field. While that's unfixed, the **pane-direct layer** and **sibling layer** can both fail for sessions started in affected environments. That's E-1408's problem to solve. Your job is to surface the existing resolver's verdict cleanly — don't try to compensate.

## Closing

- `endless task update E-1307 --status verify --outcome "<one paragraph: what shipped, the resolver layers used, the event_bridge cleanup done, tests covered, e2e verified or noted limitation>"`
- Include in your final message: "To verify: from a Claude pane, `endless session id` prints the integer EID and `ENDLESS_SESSION_ID=\"$(endless session id)\" endless task add 'X'` succeeds. From an unbound shell pane, `endless session id` errors to stderr with non-zero exit."
- Do NOT auto-land. Ask Mike.
