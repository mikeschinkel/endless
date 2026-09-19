# E-1444: Allow endless commands from a plain terminal without a Claude session

## Context

The E-1401 attribution gate in `emit_event()` refuses any event whose `actor_kind` is
`cli`/`hook` unless a session id can be resolved. From a plain terminal (no Claude
anywhere), `endless task add` and every other write currently refuses — too limiting for
triage filings from a plain shell, cron, or scripts. The gate exists for a real reason
(per-session attribution / audit trails) and should keep catching *discoverable* misuse
(Claude reachable but DB-invisible, e.g. the sandbox case → E-1585). The fix is a single
explicit escape hatch, not auto-detection.

Three design dead-ends to record and avoid:

- **Auto-downgrade based on tmux/process state.** Rejected: Claude Code can run *outside*
  tmux, and any Bash-tool subprocess inherits a no-tmux env while still being inside a
  Claude session. Structural reachability checks can't reliably distinguish
  "no Claude exists" from "Claude exists but env vars didn't propagate."
- **`--no-session` flag on every write command.** Rejected: sessions are needed for DB
  *writes*, not for any particular command shape. A 14-command per-verb decorator audit is
  the wrong scope and a maintenance hassle.
- **A "Claude pane present, want system" niche use case.** Doesn't exist coherently: if
  Claude exists, a session record must exist (or it's a bug to fix / a backfill case, not
  a flag to dodge).

## Approach

**One position-anywhere flag, parsed in the existing extractor, read at the write layer.**
Same shape as `--db`.

### 1. Top-level position-anywhere `--no-session`

Extend `DBAwareGroup.main()` in `src/endless/cli.py` (lines ~81-122) to pre-extract
`--no-session` from argv alongside `--db` and `--agent-view`. The extractor pops the flag
from any position, sets a module-level `config.NO_SESSION = True`, and hands cleaned argv
to Click — no Click option declaration on any subcommand, no per-verb decorator, no
contextvar. Mirrors the existing `agent_view` handling in the same loop (lines 105-108
and 111-112).

### 2. `config` exposes the flag

Add a module-level `NO_SESSION: bool = False` to `src/endless/config.py`, alongside the
existing `RESOLVED_CONFIG_DIR` pattern. The extractor in `cli.py` flips it. `emit_event`
reads it. One place to look.

### 3. `emit_event` reads it at the write layer

In `src/endless/event_bridge.py:emit_event()` (lines ~27-108):

- If `config.NO_SESSION` is True: **skip** the resolver block (lines 73-97) entirely and
  set `actor_kind = "system"` before reaching the gate. `system` is already exempt from
  `_ATTRIBUTION_REQUIRED`, so the event emits with no session attribution. No stderr note
  — the user asked explicitly.
- Otherwise: existing behavior. If resolution returns None and `actor_kind` is in
  `_ATTRIBUTION_REQUIRED`, raise the gate's `ClickException` (now with a fourth bullet).

### 4. Error message gets the fourth bullet

`event_bridge.py:100-108`:

```
Cannot determine the Endless session for this pane.

To fix, do one of:
  - Run this command from a Claude session pane.
  - Export ENDLESS_SESSION_ID="$(endless session id)".
  - Run `endless task bind <task-id>` from this pane to connect it to a
    sibling Claude session in the same tmux window.
  - Pass --no-session (accepted in any position) to file without a Claude
    session binding (actor.kind=system; for cron, scripts, plain-shell
    triage filings).
```

### Out of scope (intentional)

- **Claude reachable but DB-invisible** (sandbox case): the gate keeps refusing. This is
  bug-shaped, not flag-shaped. **E-1585** (filed `--cleans-up E-1444`) owns the cross-DB
  resolver / session-row mirroring work for `--db sandbox`.
- **`task claim`'s own `--force` refusal**: untouched. claim creates a worktree + changes
  status — explicit `--force` for the no-session branch stays. With this fix landing,
  `claim --force --no-session` from a plain shell will work end-to-end (today the gate
  defeats the `--force`-no-session path even when the user asked for it; the
  position-anywhere `--no-session` fixes that without claim needing any special handling).

## Implementation sites

- **`src/endless/cli.py:67-122`** — extend `DBAwareGroup.main()`'s argv loop to also pop
  `--no-session` (no value; presence sets True). One added clause matching the
  `--agent-view` shape.
- **`src/endless/config.py`** — add `NO_SESSION: bool = False` module-level (or via a
  small setter mirroring `apply_db_choice`). No new file.
- **`src/endless/event_bridge.py`** —
  - import the flag from `config`.
  - at the top of `emit_event()`'s body, before the resolver block (line ~73): if
    `config.NO_SESSION`, set `actor_kind = "system"` and skip resolution.
  - update the error string at lines 100-108 to include the fourth bullet.
- **Tests** — extend the pytest suite:
  - plain shell (no tmux, no `ENDLESS_SESSION_ID`): `endless --no-session task add "..."` and
    `endless task add "..." --no-session` (both positions) succeed; emitted event has
    `actor.kind="system"`, no `actor.session_id`.
  - plain shell without the flag: `task add` still refuses, error now lists the
    `--no-session` bullet.
  - inside a Claude pane (control): `task add` without the flag still attributes to the
    session — no regression.
  - sandbox case (synthetic — Claude pane reachable but `--db sandbox` resolver blind):
    without `--no-session` the gate refuses (regression-protects E-1585's space). With
    `--no-session` it emits as system (the flag is a generic escape, not sandbox-aware).

## Verification

1. **Primary** — bare shell, no tmux, no `ENDLESS_SESSION_ID`:
   `endless --no-session task add "probe" --type chore` succeeds; ledger event has
   `actor.kind="system"`.
2. **Position-anywhere** — `endless task add "probe" --no-session` (flag after the verb)
   also succeeds (DBAwareGroup pre-extracts).
3. **Still refuses without the flag** — same bare shell, `endless task add "probe"` fails
   with the gate error, now listing the `--no-session` bullet.
4. **No regression in Claude pane** — `endless task add "probe"` (no flag) from a Claude
   pane still attributes to the session.
5. **`claim --force` from plain shell** — currently broken (`--force`-no-session is
   defeated by the gate). With `--no-session` passed alongside, `claim --force
   --no-session` now succeeds session-less.
6. **Discoverable-misuse path preserved** — synthesize the E-1585 case (Claude pane
   reachable but resolver blind under `--db sandbox`); the gate still refuses without the
   flag.
7. `just build && just test` green.

## Related

- E-1401 — introduced the gate. This adds an explicit escape; the gate's correctness
  contract for unflagged callers is unchanged.
- E-1242 — `task claim --force` no-session precedent; its gate-defeated path becomes
  reachable via `claim --force --no-session`.
- E-1337 — `worktree land`/reaper task whose verification surfaced the gate friction.
- E-1429 / E-1476 — `--db` position-anywhere pattern this reuses (`DBAwareGroup`).
- **E-1585** — follow-up filed `--cleans-up E-1444`: cross-DB session resolver gap under
  `--db sandbox`. Out of scope here; the gate continues to surface that mismatch.
- E-1281 — self-dev DB sandbox; E-1585 is its consequence.
