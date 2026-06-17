# E-1588 — Fix `session status add` failing to resolve the session under `--db sandbox`

## Context

`endless session status add` resolves the session differently from every other
write: the Python command (`src/endless/session_status_cmd.py:118` `_resolve_process`)
just passes `TMUX_PANE` to Go, and Go re-resolves by pane
(`internal/events/session_status.go:43` `execSessionStatusRecorded` →
`liveSessionByProcessTx(p.Process)` → `SELECT id FROM sessions WHERE process=? AND
state!='ended'`). That needs a **pre-existing** sandbox row whose `process` matches
the caller's pane.

But in a self-dev worktree:
- The hook writes the session row only to **main** (`PinMainDB`), never the sandbox.
- `session status add` does **not** self-populate (unlike `task add`/`claim`, which
  go through `_current_endless_session_id` → `ensure-claude-id`).

So on a **fresh sandbox**, `endless --db sandbox session status add` errors with
`no live session for process "%NNN"` until some *other* `--db sandbox` command has
lazily created the row. This makes the feature untestable in a worktree without a
brittle prior-command dependency. (Confirmed during E-1585 verification: a
`task add` had to be run first to seed `process=%NNN`.)

The original author anticipated this: `_resolve_process` has a **reserved
`--session-id N` override** ("skip pane lookup, use this id directly", currently
erroring as unimplemented — `session_status_cmd.py:104-117`).

## Decision (decided — implement as written)

Route `session status add` through the **unified** `_current_endless_session_id()`
resolver (which now includes the E-1585 `@endless_session_uuid` window-option tier
and the CLAUDECODE tier), then pass the resolved integer **eid** to Go via the
reserved override, so Go uses the id directly instead of re-resolving by pane.

This makes the command testable in a worktree with no prior-command dependency,
works for both Claude's own pane and a sibling shell, implements the override that
was always intended for this, and keeps the hook sandbox-agnostic (consistent with
the E-1585 window-option architecture).

## Implementation

### Python — `src/endless/session_status_cmd.py`
1. In `session_status_add`, before building `payload["process"]`, resolve the eid:
   `eid = task_cmd._current_endless_session_id()` (heuristic-free; no prompting,
   preserving today's non-interactive behavior).
2. If `eid is not None`: set the override sentinel in the payload — reuse the
   `process` field with the reserved form `f"__session_id={eid}"` (matches the
   existing reserved design; no payload-schema change).
3. If `eid is None`: fall back to today's behavior (`TMUX_PANE`), so Go still emits
   its clear "no live session for process" error. Main-path behavior is unchanged
   when the resolver succeeds (it returns the same session) — cover with a parity
   test.
4. Implement the `--session-id N` flag path in `_resolve_process` (drop the
   "not implemented in v1" error): an explicit `N` produces the same
   `__session_id=N` sentinel. (Useful for test fixtures and non-tmux callers.)

### Go — `internal/events/session_status.go`
5. In `execSessionStatusRecorded`, detect the `__session_id=N` sentinel in
   `p.Process`: parse `N`, validate the row exists and `state!='ended'` (clear
   error otherwise), and use it directly — skipping `liveSessionByProcessTx`.
   Absent the sentinel, keep the current pane-lookup path unchanged.

### Tests
- Python (`tests/`): `session status add` resolves via `_current_endless_session_id`
  and emits the `__session_id=` sentinel (mock the resolver + `emit_event`); the
  `--session-id N` flag produces the sentinel; `None` resolution falls back to pane.
- Go (`internal/events/session_status_test.go`): the sentinel path uses the id
  directly; a non-existent/ended id errors clearly; the no-sentinel path resolves
  by pane as before.

## Verification

1. **Fresh sandbox in a worktree** (no prior `--db sandbox` command):
   `endless --db sandbox session status add <<'XML' … XML` → succeeds (resolves via
   the CLAUDECODE / window-option tier), records a `session_statuses` row. No
   `no live session for process` error.
2. **Sibling shell pane** (no Claude env) in the window: same command succeeds.
3. **Main path unchanged**: `endless --db main session status add` still records
   against the real (hook-created) session row.
4. `just test` + `just test-go` green (note: two pre-existing
   `TestShouldSkipForWorktreeAt_*` failures also fail on `main` — unrelated).

## Coordination

- `cleans_up` E-1585 — builds directly on its window-option resolver tier.
- `relates_to` E-1451 — same "resolve who I am for a write" surface; if/when
  E-1451's explicit `--user-session`/`--new-session` flags land, they take
  precedence in `_current_endless_session_id`, and this path inherits that ordering
  for free.
