## Runtime mechanism (from E-1815)

E-1815 brainstormed the runtime half of E-1812 — how an eligible task gets
spawned, where the selector lives, and how the user observes and throttles it.
Everything below is settled there and folds into this task rather than spawning
tasks of its own.

Scope boundary: E-1815 did NOT touch the eligibility inputs. Whether
`complexity` is a new axis or is `tasks.tier` under a better name is E-1813's
brainstorm, which blocks this task. Note in particular the gate collision E-1813
raises — `--tier 1` reaches `ready` with no human approval and no
background-session guard, while `approve` refuses a background session, so
tier-1 is currently a self-serve path into the auto-spawn candidate pool. The
selector below assumes `ready` means human-approved (ED-1538); if tier-1
survives E-1813 unchanged, the selector must exclude it explicitly.

**The selector is a registered job on the E-698 fire-once runner.** Not a new
process, not a daemon. E-1976's project monitor window fires the runner; the
runner executes the selector only when due.

**Rate throttle = the job's own cadence field.** No timer to build. The selector
spawns at most one task per due-run, so rate is a field that already exists, is
inspectable via `jobs list`, and is adjustable without touching the monitor. It
must be decoupled from the monitor's own refresh (2s is far too fast — Claude
takes tens of seconds to start working, and spawning several at once spikes CPU
to unusable). The throttle is not redundant with the cap: it protects against
CPU spikes, tmux instability (crashes have happened), contention, and it buys a
reaction window before a batch leaves the gate.

**Cap = count of AUTO-SPAWNED unverified tasks, per project.** Initially 3, and
user-adjustable. NOT total unverified: there are 57 unverified in `endless` (75
machine-wide, 71 of them 30-90 days old), which is sediment rather than a queue,
and a cap of 3 against a floor of 57 would never clear — the feature would ship
dead. Counting only auto-spawned output is self-limiting, works at any backlog
size, and scopes the governor to exactly what auto-spawn is responsible for.
Switching to a total count later is a `WHERE` clause. Draining the sediment is
orthogonal (E-1977).

Cap scope is per project. A machine-wide ceiling is deferred — roughly five
lines (same count query minus the project filter), but with `endless` dominating
so heavily the number cannot be exercised today, and an untested ceiling first
binds on the worst possible day. Six projects do have unverified work (endless
57, gomion 7, h2pp 6, go-tealeaves 3, macmail-ext 1, init 1), so multi-project is
real, just not busy.

**Stalled sessions count against the cap.** A session paused on a permission
prompt is a claim on the user's attention, and the cap measures claims on
attention — no special case. Sessions pausing until noticed is accepted
behavior. This is safe only because E-1976 makes "waiting on you" loud, which
makes that display load-bearing here rather than polish.

**`tmux new-window -d`** so a spawn never steals focus from the user's current
window. One flag; folded here rather than filed separately per ED-1550. Arguably
worth applying to manual `task spawn` too.

**Target tmux session is config, default `active`.** The selector runs in the
monitor's own tmux session, so `new-window` must name an explicit target or the
spawned windows land in the monitor session — out of sight, out of mind, which is
the exact failure mode that produced the unverified sediment. `active` is the
user's session (auto-spawned windows land interleaved with manual work,
deliberately); `monitor` is the other value. When no tmux client is attached
`active` resolves to nothing: skip the run and record why rather than guessing,
so auto-spawn naturally pauses while fully detached.

**Provenance: `spawned_by` stays null; add an `auto_spawned` flag on the SPAWNED
session.** Null is already valid for any hand-started session. The distinguishing
icon renders on the spawned session, so the fact belongs there. Rejected: the
filing session as spawner (already recorded as the `surfaced` relation, so it
duplicates an existing fact while destroying the honest one, and points at a
session that may not appear in the session list); a session row for the monitor
itself via a third `sessionkind` (navigability argument is empty — the window is
always directly reachable; state-recording argument is covered by `jobs list`
plus `ERR-NNNN` errors).

Auto-verify and auto-land of finished auto-spawned work is wanted but explicitly
downstream, as is stall detection. Neither is filed.
