# E-1585 — Populate sandbox DB's sessions table with the active Claude session

## Context

In a self-dev worktree, the per-worktree sandbox DB never gets a `sessions` row
for the **active Claude session**. The failing use-case (Mike): a *sibling* tmux
pane — a plain shell, in the **same tmux window** as the Claude session, cwd
inside that Claude session's task worktree — runs `endless … --db sandbox`. The
resolver tries to attribute the command to the sibling Claude session
(`_current_endless_session_id`, `src/endless/task_cmd.py`). A shell pane has no
`CLAUDECODE`/`CLAUDE_CODE_SESSION_ID` env (those belong to Claude's process) and
the sandbox has no row for the Claude pane, so every resolver tier fails → `None`.

## Constraints (from Mike)

- **Do not reach into `--db main`.** The fix must work entirely within the
  sandbox + tmux; no copying/reading from `~/.config/endless`.
- A sibling pane *can* read the Claude session identity from a **tmux window
  option** (window options are shared by all panes in the window). E-1128 already
  sets `@endless_task_id`/`@endless_spawned_by`/`@endless_project_id` at spawn
  time, but those are set before Claude launches (so they can't carry the new
  session's UUID), and task-id→session is ambiguous (duplicates). So add a
  dedicated `@endless_session_uuid` window option, set by Claude itself.

## Design

### Part 1 — Publish the Claude session UUID to its tmux window

In `runClaude` (`internal/hookcmd/claude.go`), after the existing
`monitor.TouchSession(...)` (~line 133), set a tmux **window** option carrying
this session's UUID:

`tmux set -w -t <TMUX_PANE> @endless_session_uuid <payload.SessionID>`

Add a small best-effort helper (mirrors `tmuxTaskID`'s exec style; no error when
not in tmux). Run it every event so it self-heals after a tmux server restart,
exactly as TouchSession does (one extra cheap `tmux` exec; the hook already
execs tmux). No DB write, no main reach. This makes the live Claude session's
UUID readable by any sibling pane in the window.

### Part 2 — Resolver reads the window option, populates + resolves the sandbox row

In `_current_endless_session_id` (`src/endless/task_cmd.py`), add a tier **after**
the pane-direct match (tier-3) and before the DB sibling-lookup (tier-4):

1. Read `@endless_session_uuid` from the current tmux window
   (`tmux display-message -p -t $TMUX_PANE '#{@endless_session_uuid}'`).
2. If non-empty, resolve via the existing `_ensure_claude_session_id(uuid)` path
   — which shells to `endless-go session-query ensure-claude-id` against the
   active DB context (the sandbox, under `--db sandbox`). That UPSERTs the
   sandbox `sessions` row for the UUID and returns its integer id. Return it.

This both *populates* the sandbox sessions table with the active Claude session
(the task's goal) and *resolves* it, unambiguously (one UUID per window — unlike
tier-4's n>1 problem), with no main reach.

**Correctness detail:** `_ensure_claude_session_id` currently hardcodes
`process = os.environ["TMUX_PANE"]`. For tier-2 that pane *is* Claude's, so it's
right. For this new tier the caller is the **sibling shell**, so its pane must
NOT be recorded as the Claude session's `process` (TouchSession's collision
invalidation would hijack/clobber). Parameterize
`_ensure_claude_session_id(uuid, process: str | None = None)`: `None` keeps the
current env-pane behavior for tier-2; the window-option tier passes `process=""`
so the row is created/kept with NULL process and resolved purely by UUID.

### Part 3 — Provision config.json in the sandbox (folded in, no main reach)

`endless --db sandbox <cmd>` currently fails before the resolver runs:
`event_bridge.py:245` `_get_or_create_node_id()` hard-requires `config.json`
(does not auto-create). Sandbox setup never writes it. Fix in the sandbox
provisioning path `seedFromWorktree` (`internal/sandboxcmd/seed_worktree.go`),
alongside the DB + project-row seeding: write `<sandbox>/endless/config.json`
with the minimal default (`{"roots":["~/Projects"],"scan_interval":300,
"ignore":[]}`), mirroring Python `DEFAULT_CONFIG`. `node_id` is added lazily by
`_get_or_create_node_id` on first event. Generated locally — no read of main's
config. (No retroactive migration: one user, unshipped; existing sandboxes get
the file via re-init or the one-time verification step below.)

## Critical files

- `internal/hookcmd/claude.go` (~line 133) — set `@endless_session_uuid` each event.
- `src/endless/task_cmd.py` — new window-option resolver tier; parameterize
  `_ensure_claude_session_id` with `process`.
- `internal/sandboxcmd/seed_worktree.go` — write default config.json at seed.
- Reuse: existing `_ensure_claude_session_id` / `session-query ensure-claude-id`
  (Go `monitor.EnsureClaudeSessionID`), `tmuxTaskID` exec pattern.

## Tests

- `internal/sandboxcmd/seed_worktree_test.go` — assert config.json written after
  `seedFromWorktree` (valid JSON, expected keys).
- `src/endless/` Python test — `_current_endless_session_id` resolves via the
  window-option tier (monkeypatch the tmux read + `_ensure_claude_session_id`),
  and that the tier passes `process=""` (does not record the caller's pane).
- New-session UUID publish: add/extend a hook test for the helper if feasible;
  otherwise cover via the verification steps (tmux side-effects are awkward to
  unit-test — note this explicitly rather than silently skipping).

## Verification (in the worktree; NEVER `just install` from a worktree — the live
session's hook is `<worktree>/bin/endless-hook` via `.claude/settings.json`)

1. `just build` — rebuilds `bin/endless-hook` + `bin/endless-go`; the live hook is
   picked up on the next event.
2. Provision config.json for this already-created sandbox once (no migration):
   write the default to `~/.cache/endless/sandboxes/e-1585/endless/config.json`.
   (New sandboxes get it automatically via Part 3.)
3. Trigger a hook event (any tool call), then confirm the window option is set:
   `tmux display-message -p -t $TMUX_PANE '#{@endless_session_uuid}'` → the real
   `CLAUDE_CODE_SESSION_ID`.
4. From a **sibling shell pane** (no Claude env) in this tmux window, cwd in the
   worktree, run a resolver-dependent command that goes through the Python
   resolver (the `emit_event` path), e.g.
   `env -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID -u ENDLESS_SESSION_ID \
     endless --db sandbox task add "Add a probe"` → gets past session resolution
   (no "no session" error). Confirm the sandbox now holds the Claude session:
   `sqlite3 ~/.cache/endless/sandboxes/e-1585/endless/endless.db \
     "SELECT id,session_id,process,state FROM sessions"` → a row with the real
   `CLAUDE_CODE_SESSION_ID`.
5. `just test` (Python) + `just test-go` green (two pre-existing
   `TestShouldSkipForWorktreeAt_*` failures exist on `main` too — unrelated,
   `os.Executable()`-dependent).

## Resolution-path note (discovered during verification)

`endless session status add` does NOT use the Python `_current_endless_session_id`
resolver — it resolves the session in Go by tmux-pane (`execSessionStatusRecorded`
→ `liveSessionByProcessTx(p.Process)`, `internal/events/session_status.go`). So:

- The window-option tier fixes the **Python resolver** path: `task add/update/
  claim` and anything via `emit_event` from a sibling shell. ✓
- The Claude session's OWN `--db sandbox session status add` works once any of
  its `--db sandbox` commands has run (tier-2 sets `process=<pane>` on the row);
  verified end-to-end.
- A *sibling shell* driving `session status add` specifically would need the
  resolved eid threaded into the Go event (the handler resolves by the caller's
  pane). That's a non-use-case (status records a session's OWN state) and a
  separate concern — out of scope for E-1585. Flag for Mike.

## Coordination with E-1451 (Mike: "proceed + coordinate")

E-1451 (non-Claude shell ledger writes, `harness=shell`, explicit
`--user-session`/`--new-session`/`session start`) overlaps the resolver but is
complementary:

- **Layer below explicit flags.** The new window-option tier lives in
  `_current_endless_session_id`'s heuristic chain. E-1451's future explicit
  flags must take precedence over it; the window-option tier stays the fallback.
- **Discovery precondition.** E-1451 must not land until session discovery is
  rock-solid; E-1585 modifies discovery, so the two are linked. Add a
  `relates_to` link E-1585↔E-1451 during implementation.
- **nil-UUID semantics** (placeholder vs E-1451's reserved `--user-session`
  UUID) are left to E-1451/E-1507 — not touched here.

E-1431 (session archive) — no conflict; `@endless_session_uuid` is in fact
useful to its `claude --resume <uuid>` restore path.

## Notes

- **The nil-UUID placeholder is load-bearing, NOT vestigial** (Mike): it serves
  the *no Claude session at all* case — a bare shell in the worktree resolves to
  the seeded placeholder via the wrapper's `ENDLESS_SESSION_ID` export. It is
  left untouched. The two paths coexist: no Claude session → placeholder; sibling
  Claude session in the window → real row via the `@endless_session_uuid` tier.
- One Claude session per window assumed (the sibling-pane use-case). Two Claude
  panes in one window would share the single `@endless_session_uuid`; out of
  scope here — flag only.
- E-1128's existing options (`@endless_task_id` etc.) are left untouched.
