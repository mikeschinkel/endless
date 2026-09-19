# E-1681 — `endless session goto` / `session back` (tmux session navigation)

## Context

The `session next` view (E-1465) lists in-flight sessions as `⟳` rows keyed by the
task each is working, but there's no way to jump tmux focus to one, and a spawned
session's only way "home" is the literal `tmux switch-client -t %NNN` baked into
its handoff. This task adds:

- `endless session goto <task|session>` — switch the tmux client's focus to the
  target session's pane.
- `endless session back` — return browser-style via a back-stack.

## Design (decided with Mike)

Two concerns were separated:
1. **Back-stack** (drives `back`) — ephemeral, server-life navigation state. **In
   scope here.**
2. **Durable navigation trail** (analysis / recovering a lost session, captures
   manual moves too) — **out of scope; filed as a follow-up** (`--cleans-up E-1681`).

### Back-stack
- **Storage:** a **tmux server option**, keyed per attached client:
  `@endless_backstack_<sanitized client_name>` (value = space-separated tokens,
  top of stack = last token). Falls back to a global `@endless_backstack` when no
  client name is resolvable. Lifetime matches the tmux server, which is exactly as
  long as the pane ids inside it are valid. No file, no DB, no schema change.
  (Sessions writes are Go-only and tmux options are already read/written from
  Python — `@endless_spawned_by`, `@endless_session_uuid` — so this is the
  least-machinery right-sized tool for ephemeral nav state.)
- **Unit = endless session id**, not raw pane (Mike's point: panes are volatile,
  a spawner can restart into a new pane under the same session id). A stack token
  is a session id (digits) resolved to its *current* live pane at pop-time. A
  source location that isn't a tracked session (e.g. a plain shell pane) is stored
  as a raw pane id (`%NNN`) and used directly — the leading `%` distinguishes the
  two token kinds.
- **Scope = the navigator = the attached tmux client.** For a single-terminal
  setup this coincides with "global per tmux server."
- **Capture = `goto` only** (predictable: `back` undoes your last `goto`; manual
  `select-window` moves don't touch it — capturing those belongs to the durable
  trail follow-up).

### `session goto <ref>`
1. Require tmux (`$TMUX`); else friendly error.
2. Resolve `<ref>` → target session → its live pane (rules below); friendly error
   on no-match / ambiguous / no-live-session.
3. Resolve the current pane (`$TMUX_PANE`) → its session id if tracked, else keep
   the raw pane id; push that token onto this client's stack.
4. `tmux switch-client -t <target-pane>`. If the target pane is gone, error and
   pop the token we just pushed (don't leave it dangling).
5. Status to stderr: `• goto E-NNNN → session E-MM (pane %214)`.

Ref resolution (reusing the Go-backed `_live_sessions` read — no new Go):
- `E-NNNN` → task id → live sessions with that `active_task_id`, most recent
  (`last_activity DESC`, already the Go order); none → "no live session on E-NNNN".
- bare digits `N` → if a live session has `endless_session_id == N` **xor** a live
  session has `active_task_id == N`, use the unique match; if **both** match
  different sessions → ambiguous error telling the user to write `E-N` for the task
  (Mike confirmed: ambiguous → error).
- non-numeric → treat as a session ref: prefix-match `harness_session_id` (uuid),
  like `session cd`.
- session ref → that session's `pane_id` (its `sessions.process`).

### `session back`
1. Require tmux; resolve this client's stack.
2. Pop the top token:
   - `%pane` token → use directly; if the pane no longer exists, drop and pop next.
   - session-id token → resolve to that session's *current* live pane; if the
     session is no longer live, drop and pop next.
3. If the stack drains → **spawn-back fallback**: read this window's
   `@endless_spawned_by` (set at spawn = spawner's session id; `task_cmd.py:4441`)
   and switch to that session's current live pane. Live re-resolution is a strict
   superset of seeding (handles a spawner restarting into a new pane) with zero
   change to the spawn path.
4. `tmux switch-client -t <pane>`.
5. Nothing left and no live spawner → message "no previous session".

## Files

- `src/endless/cli.py` (~line 880, in the `session` group) — register
  `@session_cmd.command("goto")` (one positional `target_ref`) and
  `@session_cmd.command("back")`, each delegating into `session_cmd.py`.
- `src/endless/session_cmd.py` — add `session_goto(target_ref)`, `session_back()`,
  and helpers:
  - `_resolve_goto_target(ref, live)` → `(target_pane, label)`.
  - `_backstack_key()` (client name → sanitized option suffix),
    `_backstack_read()/_write()/_push()/_pop()` (tmux option get/set).
  - `_tmux_switch_client(pane)`, `_pane_exists(pane)` (subprocess wrappers,
    alongside existing `_tmux_window_pane_ids`).
  - `_session_pane(live, session_id)` (session id → current live pane),
    `_spawner_pane(live)` (read `@endless_spawned_by` → live pane).
  - Reuse `_live_sessions`, `_project_root_from_cwd`-style resolution as `session
    cd` does.

## Tests — `tests/test_session_goto_back.py` (model: `tests/test_session_cd.py`)

Mock `_live_sessions` + `subprocess.run` (tmux), assert the exact tmux commands:
- goto by task id → pushes current session token, switches to the live session's pane.
- goto by session id / uuid prefix → switches to that pane.
- goto unknown / task with no live session → friendly error, no switch, nothing pushed.
- goto ambiguous bare number → error naming `E-N`, no switch.
- back pops the pushed token (session id → current pane) and switches.
- back empty stack but `@endless_spawned_by` set → switches to spawner's pane.
- back empty stack, no live spawner → "no previous session".
- back drops a stale token (dead pane / ended session) and pops the next.
- both verbs outside tmux → friendly error.

## Verification (end-to-end)
1. `just build` in the worktree (current candidate `bin/endless-go`).
2. `just test` — new suite green, no regressions.
3. Manual, inside tmux from the worktree (sandbox-routed): `goto` a live
   task/session → focus moves; `back` → returns; from a spawned session's pane,
   `back` with empty stack → returns to the spawner; `back` from a fresh pane →
   "no previous session".

## Out of scope (follow-up filed)
- Durable navigation trail (DB, captures manual moves via tmux hooks, queryable
  for usability analysis / finding lost sessions) — separate task, `--cleans-up E-1681`.
- The `session next` view itself (E-1465).
