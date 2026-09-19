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
