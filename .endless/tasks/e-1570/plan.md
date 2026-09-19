# E-1570 — Add endless task spawn --attach and endless task attach for bg-agent viewing

Lands after **E-1568** (bg dispatch writes `short_id`). Two attach verbs.

## Decisions

### `endless task spawn --attach <id>` — new tmux window
- **Modifier on spawn**, not a dispatcher. Requires the target task to already have a `kind_id=2` (background; FK to `session_kinds`) row in `sessions` with a populated `short_id`. Does NOT dispatch a new bg agent.
- Mutually exclusive with `--bg` (you either dispatch or attach, not both — chaining is "spawn --bg then spawn --attach", explicit two calls).
- Flow:
  1. Lookup: `SELECT short_id FROM sessions WHERE active_task_id=? AND kind_id=(SELECT id FROM session_kinds WHERE slug='background') AND state='working' ORDER BY id DESC LIMIT 1`. (The subselect keeps the seed-id stable; if `session_kinds` row ids ever change, the lookup still resolves.) If no row → ClickException: *"E-{id} has no live bg agent. Dispatch with `endless task spawn --bg E-{id}` first."*
  2. Build window name with existing `_spawn_window_name(...)` helper (so the window is recognizable as "this task's attach window").
  3. `tmux new-window -n <window_name>`.
  4. Optionally `tmux set -w -t <window_name> @endless_attached_short_id <short_id>` for diagnostics — not load-bearing.
  5. `tmux send-keys -t <window_name> "claude attach <short_id>" Enter`.
  6. Print return-line hint (the user can detach with `←` / `Ctrl+Z`, which the research doc documents).

### `endless task attach <id>` — exec current pane
- New top-level subcommand under `endless task` (no `spawn` prefix — this is its own verb, not a flag).
- Replaces the current process with `claude attach <short_id>` via `os.execvp`. Documented as "use from a fresh shell" — if used from inside an active Claude/coordinator pane, the user kills their own coordinator. We do **not** add a confirm prompt; explicit verb name carries the meaning.
- **Go-port note:** when this verb migrates from Python to Go, substitute `syscall.Exec(path, []string{"claude", "attach", shortID}, os.Environ())` after `exec.LookPath("claude")`. POSIX `execve` semantics are identical; only PATH lookup is now explicit. No design change.
- Flow:
  1. Same DB lookup as above. Error message tailored: *"E-{id} has no live bg agent."*
  2. If `os.environ.get("CLAUDECODE") == "1"` (the documented Claude-spawned subprocess marker, research doc §6) → warn loudly to stderr: *"You are inside a Claude session. `endless task attach` replaces the current process; you will lose this session. Re-run with --force to proceed, or open a fresh terminal."* Then exit non-zero unless `--force`.
  3. Otherwise `os.execvp("claude", ["claude", "attach", short_id])`. No return — process is replaced.

### Resolving "current view" for status-line
When the coordinator runs `task attach E-CHILD`, the coordinator's *separate* fg session is unaffected. The newly-attached pane is a fresh process whose own SessionStart hook fires. That hook's bg-context detection (added in E-1568) recognizes `CLAUDE_JOB_DIR`, finds the bg row, and decorates with `last_activity`. No new sessions row gets inserted.

For `spawn --attach`, the new tmux window's pane *also* runs `claude attach` — same hook path, same outcome.

The coordinator's `sessions.active_task_id` only changes when the **user explicitly** runs `endless task claim` from the coordinator pane (existing flow). E-1570 doesn't tweak active_task_id implicitly.

### `claude attach` resolution
Reuse the binary resolution from `spawn_plan` (lines 3398–3400): `~/.local/bin/claude` if present, else `claude`. Extract to a small helper `_claude_binary()` so both spawn and attach call it — drive-by refactor, isolated.

## Files

### Changed
- `src/endless/task_cmd.py`:
  - `spawn_plan` gains `attach: bool = False` and a `--attach` click option. Validate mutual exclusion with `--bg`. Branch at the top — if `attach`, run the attach flow (lookup, tmux new-window, send-keys) and return; skip all pre-claim/dispatch logic.
  - New click command `task_attach(item_id, force)` registered as `task_cmd.command("attach")`. Implements the exec flow.
  - New helper `_claude_binary() -> str` (extract from `spawn_plan`).
  - New helper `_lookup_bg_short_id(task_id: int) -> str | None` — single source of truth for the SELECT, reused by both attach verbs.

### Tests
- `tests/test_task_attach.py` (new):
  - `task attach` with no bg row → exits non-zero with message.
  - `task attach` from CLAUDECODE=1 env → refuses without --force, accepts with --force (mock `os.execvp`).
  - `task attach` happy path → `os.execvp` called with `("claude", "claude", "attach", "<short_id>")`.
  - `task spawn --attach` with no bg row → exits non-zero.
  - `task spawn --attach` happy path → mocks subprocess; asserts `tmux new-window` then `tmux send-keys ... "claude attach <short>"`.
  - `task spawn --bg --attach` → mutual-exclusion error.

## Sequence note

Depends on **E-1568** (short_id column populated). Independent of E-1567 / E-1572 / E-1573.

## Verification

1. `just test` passes.
2. End-to-end: claim epic, `spawn --bg <child>`, `spawn --attach <child>` → new tmux window opens running the bg agent.
3. Detach with `Ctrl+Z`; the bg agent keeps running (`claude agents --json` still lists it).
4. From a fresh terminal: `endless task attach E-{child}` drops you into the bg agent in-place.
5. Inside the coordinator session: `endless task attach E-{child}` refuses unless `--force`.
