# E-1697 — Fix session surfaces resolving against the sandbox DB from a worktree cwd

## Context

Inside a self-dev worktree the tmux status line shows the dim placeholder `·` and
session-resolving surfaces fail, because a **cwd-self-detected sandbox** defeats
the main-DB pin that session/pane surfaces are supposed to have. Session state is
real-world activity and lives in the **main** DB (`~/.config/endless/endless.db`)
regardless of cwd; the per-worktree sandbox holds only the dev task ledger.

This is **one defeated invariant, not three surface bugs.** The intended rule:

> Real session/pane state lives in main. Every surface touching it pins main by
> default. The ONLY thing that routes such a surface to a sandbox is an explicit
> `--db` / `--config-dir` flag (deliberate testing). A cwd-self-detected sandbox
> must never route a session surface away from main.

**The bug (confirmed in code):** `SelfDetectWorktreeSandbox()` (db.go:262) routes a
worktree-cwd process to its sandbox by calling `SetDBContextDir(sandboxDir)`,
setting `dbContextDir`. `HasExplicitDBContext()` (db.go:124) returns
`dbContextDir != ""` — it cannot tell a cwd-self-detect from an explicit
`--config-dir`. The dispatcher pins main for `hook`/`channel`/`tmux` only
`if !HasExplicitDBContext()` (main.go:120). So a self-detected sandbox reads as
"explicit" → the pin is **skipped** → every session surface reads/writes the
sandbox.

### What each of Mike's four questions established
1. **`tmux status-line` invoker:** only the tmux `status-format[1]` string baked by
   `endless tmux apply` (apply.go:75): `<binPath> tmux status-line --pane=#{pane_id}`
   — one global config, no `--config-dir`. Baking `--db main` there would be a
   per-caller selection surface for an always-main value (anti-pattern) and would
   only patch tmux, not the hook/channel. → fix the invariant, not the caller.
2. **`esu` is already safe:** `_endless_run` forces `--db main` on every call
   (cli.py:558,562, E-1591). `esu` reads main correctly; it is NOT a read-routing
   victim. Its only failure mode is that the session ROW isn't in main — a *write*
   problem. **No Python change** (Mike's decision: guard fix only; direct non-esu
   `endless session show/use/cd` is treated as unsupported).
3. **The failing WRITE is session registration by the hook** (`TouchSession` /
   `InitSession` / `BindSessionToTask`, claude.go:161/178/212). Contrary to the
   original plan, `hookcmd` never calls `ForceRealDB()` (only `sandbox_test.go`
   does) — the hook relies on the dispatcher's `PinMainDB()`, defeated by the same
   self-detect. Evidence: this worktree's session `%299` exists ONLY in the sandbox
   (id 2), absent from main's 841 rows. `%286` (E-1461) is in main because its
   first hook fire predated its sandbox; spawned sessions get the sandbox first, so
   the hook writes them to the sandbox. The guard fix lands registration in main
   regardless of timing.
4. **Creation stays selective, sandbox testing preserved:** the discriminator is
   the explicit flag. Real session (no flag) → main; explicit `--db sandbox` /
   `--config-dir` (tests) → sandbox; non-self_dev → self-detect no-ops → main.
   A session row in the **sandbox is correct and must stay** — sandbox task ops
   (`ensure-claude-id`) need a local session row for the sandbox's session→task FK.
   So the fix must NOT touch `ensure-claude-id` / `start_chat`; it changes only
   `hook`/`channel`/`tmux` invoked without an explicit flag.

## Fix (single change) — `internal/monitor/db.go`

Make `HasExplicitDBContext()` reflect **only an explicit `--config-dir`**, not a
cwd-self-detected sandbox.

- Add package var `dbContextFromFlag bool` (distinct from the gate helper
  `dbContextExplicit()` at db.go:195, which stays as-is — a self-detected sandbox
  is still a valid context for *opening* the DB).
- `ConsumeDBContextFlag()` (db.go:169): after each `SetDBContextDir(...)`, set
  `dbContextFromFlag = true`.
- `SelfDetectWorktreeSandbox()` (db.go:282): unchanged — `SetDBContextDir` only,
  flag stays false.
- `HasExplicitDBContext()` (db.go:124): `return dbContextFromFlag`.
- Refresh the doc comments on `HasExplicitDBContext`, `SelfDetectWorktreeSandbox`,
  and the main.go:112-123 pin block: a self-detected sandbox no longer suppresses
  the pin, so hook/channel/tmux keep session reads AND writes on main from a
  worktree cwd; correct the stale "hookcmd calls ForceRealDB" rationale.

**Resulting behavior**
- self-detect + `hook`/`channel`/`tmux` → `HasExplicitDBContext()` false →
  `PinMainDB()` runs → DB reads/writes **main**; `ConfigDir()` (config.json/logs)
  still follows the sandbox via `dbContextDir` (E-1450 split intact).
- explicit `--config-dir` (tests/sandbox tooling) → flag true → pin skipped
  (unchanged).
- non-pinned subcommands from a worktree (`event emit`, `session-query`
  ensure-claude-id) → still route to the sandbox via `dbContextDir` (unchanged);
  sandbox task-ledger session rows keep working.

Blast radius: `HasExplicitDBContext()`'s only production caller is the main.go pin
guard (grep-confirmed). `test_seam.go` sets `dbContextDir` directly (flag stays
false) and is unaffected.

## Explicitly NOT changed
- Python `session_cmd.py` resolvers (Mike: guard fix only; `esu` already pins main).
- `session-query ensure-claude-id`, `start_chat`, JSONL sync — sandbox session
  rows are correct there and required for sandbox FK integrity / testing.
- `channel` — fixed for free by the guard change (already in the pin set).

## Tests — `internal/monitor` (mirror TestForceRealDB reset pattern)
- `HasExplicitDBContext()` is **false** after a self-detect proxy
  (`SetDBContextDir(dir)` with the flag untouched) and **true** after
  `ConsumeDBContextFlag()` consumes a `--config-dir`.
- With self-detect set + `PinMainDB()` (what main.go does when the guard is false),
  `DBPath()` resolves to `~/.config/endless/endless.db` while `ConfigDir()` stays
  the self-detected dir — the E-1450 split.
- Reset `dbContextDir` / `dbContextFromFlag` / `dbPathOverride` in `t.Cleanup`.

## Verify (manual)
```
cd <worktree> && just build                         # rebuild so the hook + tmux binary carry the fix
# trigger one hook fire (any tool use in the live session) so %299 re-registers
sqlite3 ~/.config/endless/endless.db "SELECT id, process, state FROM sessions WHERE process='%<claude-pane>';"
                                                    # → a row now exists in MAIN (write fixed)
env -u XDG_CONFIG_HOME ./bin/endless-go tmux status-line --pane=%<claude-pane>   # resolves the session (not ·)
cd ~ && env -u XDG_CONFIG_HOME <worktree>/bin/endless-go tmux status-line --pane=%<claude-pane>
                                                    # identical output — routing no longer depends on cwd
```
`go test ./internal/monitor/...` for the unit tests; `just test` for regression.

## Out of scope
Renderer, pane resolution, and the session row itself are correct. This is purely
DB-context routing. Task→session binding (whether `%299` shows `[E-1697]` vs
"claim a task") is orthogonal — the fix restores correct DB resolution, not the
binding.
