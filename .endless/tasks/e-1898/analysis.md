# Analysis — the dead-pane reaper can destroy live pane bindings project-wide

## Two defects, both required for the damage

### 1. The reaper trusts an ambient tmux view it never validates

`monitor.ReapDeadTmuxPanes` (internal/monitor/reap.go) decides liveness with:

    exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}")

It inherits whatever `$TMUX` the calling process has. Any caller running under a
DIFFERENT tmux server — a private `tmux -L <socket>` server started by a test or
a verify script — sees only that server's panes. Every real pane then looks dead
and the whole project is reaped. There is no check that the observed pane set is
plausible for the rows about to be ended.

The `len(alive) == 0` branch is the sharpest edge: it nulls `process` for EVERY
non-ended tmux-format row in the project in one statement. Its comment treats an
empty pane set as proof that everything is dead; it is equally consistent with
"I am looking at the wrong server."

`internal/tmuxcmd/reset.go` already guards its own entry point (refuses when
`$TMUX` is empty, calling it "a footgun"). The SessionStart caller in
`internal/hookcmd/claude.go` has no such guard, and that is the one that fires
constantly.

### 2. TouchSession cannot undo it

    process = COALESCE(NULLIF(?, ''), process)
    state   = CASE WHEN sessions.state = 'ended' THEN 'needs_input' ELSE state END

A later hook firing with an EMPTY `TMUX_PANE` flips the row back to
`needs_input` but leaves `process` NULL. The row now reads as alive and resolves
to nothing, permanently — pane lookup keys on `process`. Only a hook that fires
WITH a pane can heal it, which is why sessions actively running tools recovered
and idle ones did not.

## Observed damage (2026-08-05)

- 27 live Claude panes had their session row at `state='ended'`, `process` NULL,
  while the pane was demonstrably alive and working in that task's worktree.
- 59 of 61 tmux windows rendered a blank status line simultaneously.
- Only 3 sessions were intact — exactly the ones firing hooks with `TMUX_PANE`
  set at the time.
- Reap batches are visible in the ledger by minute, including one batch of 37.

The blank bar is invisible by construction: `runStatusLine` maps every failure
to the same placeholder (E-1895), so this destroyed state for hours with no
diagnostic anywhere.

## Absorbed from E-1895: the failure was invisible by construction

`internal/tmuxcmd/status_line.go` `runStatusLine` maps EVERY error from
`monitor.GetPaneStatus` to `placeholder()` and stays silent on stderr:

    status, err := monitor.GetPaneStatus(pane)
    if err != nil {
        // Real error (DB unreachable, etc.). Render placeholder; stay
        // silent on stderr to avoid log spam during interactive use.
        fmt.Print(placeholder())
        return
    }

`monitor.DB()` alone has ~7 fail-closed exits behind that single `err` (the
schema apply, five enum VerifyIntegrity gates, guardWorktreeDBContext). All of
them render as the same dim dot as "this pane has no Endless context", and write
nothing anywhere. That is why the 2026-08-05 incident destroyed state for hours
with no diagnostic, and why three separate hypotheses had to be eliminated
experimentally before the cause was found.

The stderr silence is CORRECT and must stay: the bar re-execs this binary once
per pane every `status-interval` (2s across ~14 panes), so logging would be a
firehose painted over a live TUI. `faults.Record` is the right shape instead —
it dedupes an open incident in place, so a bar blanking every 2s across every
pane raises ONE incident. `cmd/endless-go/main.go` already calls `faults.Bind`
for every subcommand, so the recorder is available with no new wiring.

Scope note: this is NOT E-1884 (converting 42 `log.Printf` sites that write too
noisily). This is the inverse — a site that writes nothing when it should.

## Directions (not locked — this needs a plan)

- Refuse to reap on an implausible pane set: if `alive` is empty, or disjoint
  from every stored `process` for the project, treat it as "wrong server" and
  no-op rather than reaping.
- Pin the server identity: record which tmux server a session belongs to
  (socket path) and only reap rows for that server.
- Make the damage recoverable: reviving `ended` to `needs_input` while leaving
  `process` NULL should not be possible — either restore both or neither.
- Consider whether SessionStart is the right trigger at all, given it fires from
  contexts whose `$TMUX` is not the user's server.


# Absorbed tasks (E-1642, E-1468, E-1408)

This task became the umbrella for session<->pane binding integrity. Three
pre-existing tasks were folded in and marked obsolete; their content is
preserved below so nothing is re-derived.

## From E-1642 — close sessions on idle-timeout instead of mass-end sweeps

Original description: SessionEnd/EndSession rarely fires (the harness is killed,
not cleanly exited), so sessions linger until a later reaper/collision sweep
marks a batch 'ended' at one shared instant. last_activity is bumped far past
real life, durations span days, and duration stops being a usable "real session"
signal. End sessions on last_activity + idle-timeout rather than wall-clock
sweep time.

CORRECTION, from the 2026-08-05 incident: E-1642's plan opens with a "Recon
correction" asserting there is NO reaper and that clustered mass-end timestamps
come from bulk/collision events only. That is wrong. monitor.ReapDeadTmuxPanes
IS a reaper; it is not periodic but fires on EVERY SessionStart
(internal/hookcmd/claude.go:185) and mass-ends by design — the `len(alive) == 0`
branch ends every tmux-format row in the project in a single UPDATE. The batch
of 37 rows sharing one timestamp is that branch, not a bulk event. Any plan
carried forward from E-1642 must be re-read against this.

E-1642's plan as filed, preserved verbatim:

# E-1642 — Close sessions on idle-timeout (there is no reaper today)

**Independent.** · **Layer:** Go (`internal/monitor/session.go`) + a trigger

## Recon correction
There is **NO periodic reaper**. Sessions reach `ended` only via `EndSession` on `SessionEnd`
(`session.go:511-523`, rarely fires — harness is killed, not cleanly exited) or collision-
invalidation (`session.go:280-292`, inert when `process` is empty). So real dead sessions linger
`working`/`idle` indefinitely; the clustered "mass-end" timestamps come from bulk/collision
events, not a reaper. The fix is to **add** truthful idle-timeout ending.

## Why it matters
`duration` is currently a lie (a real session can span days), so it can't signal "the real
session." E-1645's resolver and the dashboard both want reliable `state` + `last_activity`.

## Approach
1. **Idle-timeout sweep:** any session in `working`/`idle`/`needs_input` whose `last_activity` is
   older than a configurable window (e.g. 60-90 min; align with the ~1h bg idle) → mark `ended`
   with end == **`last_activity`, not `now()`** (don't bump the timestamp). `EndSession` today
   updates `last_activity` (511-523) — the idle path must NOT.
2. **Trigger** (no daemon exists): BOTH (a) a cheap project-scoped stale-sweep piggybacked inside
   `TouchSession` (runs every hook event → self-heals during activity), and (b) an explicit
   `endless session sweep [--dry-run]` for manual/cron/tmux use.
3. **Config:** idle window in `.endless/config.json` (go-cfgstore) with a sane default.

## Risks
- Don't end a session legitimately parked on `needs_input` — longer timeout/exemption for it.
- Harness resumes on wake (v2.1.142+); base the decision on `last_activity` heartbeats, not
  wall-clock since spawn, so a machine-sleep gap doesn't end a live session.

## Verification — create a per-task verify script
Create `tests/tasks/e-1642-verify.sh` in the `e-1624-verify.sh` shape (bash, `set -u`, the
`section`/`report_*`/`summary` helpers, `cd` repo root, ensure `go.work`, exit 0/1/2). Drive the
REAL code path via Go tests in `internal/monitor` plus the `endless session sweep` CLI.

The script asserts (named checks):
- `internal/monitor` compiles.
- A Go test drives a `working`/`idle` session whose `last_activity` is older than the timeout and
  asserts it flips to `ended` with end == the **historical** `last_activity` (NOT `now()`).
- A recent (within-timeout) session is left untouched.
- A `needs_input` session honors its longer leniency (not ended at the short timeout).
- `endless session sweep --dry-run` lists **exactly** the stale sessions it would end (seed via
  fresh ids); `internal/monitor` full suite stays green.

Re-runnability (sandbox NOT wiped between runs):
- Derive seeded ids/`session_id`/`short_id` from freshly-allocated ids each run; set
  `last_activity` relative to a computed "now minus N" each run, never a fixed timestamp.
- Capture seeding output; `exit 2` loudly on any setup/seed error (never /dev/null).
- Run the script at least TWICE before declaring it done.

**Verify-handoff — tell the user to verify with (only) this:**
```
esu && ./tests/tasks/e-1642-verify.sh
```




## From E-1468 — collision invalidation ends rows with no liveness check

internal/monitor/session.go collision invalidation sets state='ended' for every
other sessions row sharing the incoming process (pane) string, with no liveness
check. When two Claude session identities share one physical tmux pane (e.g. a
worktree-keyed session and a main-checkout-keyed session during a land run from
main), the live session is falsely marked ended. Repro: E-1459 land run from the
main checkout spawned queue-operation session a27e5710 on pane %203, flipping
live session 510 to ended. Proposed fix: verify the prior occupant is actually
stale (tmux list-panes / last_activity recency / project-dir match) before
invalidating.

Note its description is partly stale: it says "TouchSession upserts never
restore state", but E-1686 has since added the ended -> needs_input revive. The
liveness-check principle still stands, and is the SAME principle the reaper
needs — which is why these belong together.

## From E-1408 — SessionStart must not silently capture an empty TMUX_PANE

The SessionStart hook reads TMUX_PANE via os.Getenv and writes it to the
companion file; PaneID has json:omitempty so an empty value drops out of the
JSON entirely. Downstream, sibling detection never matches and `endless task
bind` fails. Mike's directive on that task: silent degradation is unacceptable —
the hook MUST fail when it cannot capture pane_id, and subsequent hooks should
keep failing until resolved. Evidence then: TMUX_PANE='' in both the Claude env
and the parent shell env despite TMUX being set.

This is the upstream cause of the incident's unrecoverability: TouchSession
writes `process = COALESCE(NULLIF(excluded.process,''), sessions.process)`, so a
hook firing with an empty TMUX_PANE revives `state` while leaving `process`
NULL — the row then reads alive and resolves to nothing, permanently.

## Related, NOT absorbed

E-1395 (unverified) fixed the Python `_tmux_window_pane_ids` running `tmux
list-panes` without `-t`. Same bug class as the reaper's ambient view; precedent
for the fix, no action needed here.

## Post-land defect: every live session is returned twice

Found 2026-08-13, hours after land, when `esu` refused to resolve with
"Multiple sibling Claude panes in this window" listing the SAME pane twice —
identical session id, pane, uuid and worktree. `session-query list-live` returns
114 rows for 57 sessions: every session duplicated exactly 2x.

Root cause is `live_processes`, not the pane bindings this task fixed:

- `tmux list-panes -a` lists a LINKED window's panes once per tmux session the
  window is linked into. Measured here: 442 rows for 245 distinct pane ids.
- `refreshLiveness` inserts one `live_processes` row per returned pane, and that
  temp table has no uniqueness constraint.
- `session_liveness` LEFT JOINs `live_processes` on
  (kind_id, server_uuid, address), so a pane recorded N times multiplies its
  session's row N times — and every consumer of the view inherits it.

Not the servers: only one socket in the dir answers `show-options -gv
@server_uuid`, so `observed_servers` holds a single row. The multiplier is
purely the pane snapshot.

The identity is already stated for the durable table — `processes_identity`
indexes (kind_id, ifnull(server_uuid,''), address) precisely because SQLite
treats each NULL as distinct. The observation table expresses the same identity
and does not enforce it. A pane seen twice in one snapshot is one pane.

Fix direction: give `live_processes` the same expression-based unique index and
insert with OR IGNORE (or dedupe in `parsePaneList`). Worth a regression test
that seeds a duplicate pane and asserts the view still yields one row per
session, since this is invisible until a window happens to be linked.

User-visible impact meanwhile: `esu` cannot auto-resolve a sibling pane at all,
so entering a worktree by session is blocked.
