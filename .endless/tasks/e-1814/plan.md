# E-1814 — auto-spawn eligible ready tasks

Implements ED-1538 (as amended under E-1991) on the runtime design E-1815
settled; that design is in this task's analysis. This plan records what changed
since the task was filed, the decisions Mike made while it was planned (ES-1248,
2026-09-29), and the build.

## 0. What changed since filing

- **Ratings exist.** E-1813 landed `tasks.complexity_id` / `tasks.risk_id`
  (the `internal/rating` enum; low=1, medium=3, high=5) and removed `tasks.tier`.
  The analysis's tier-1 "gate collision" paragraph is moot: tier and its
  auto-`ready` edge are gone, so `ready` means human-approved with no exception.
- **Four conditions, not three.** ED-1538 was amended to status = `ready`,
  complexity below threshold, risk below threshold, phase in (now, urgent). The
  description names only the last three.
- **Nothing is eligible yet.** 0 of the 36 `ready` tasks in `endless` are
  rated. They were approved before ratings existed, and are not rated
  retroactively here. A task becomes eligible when someone rates it.
- **The kill switch moved.** Every live view (session monitor, project
  monitor, errors) fires `jobs.RunDue`, so "monitor window open == auto-spawn
  on" no longer holds. Replaced by per-project opt-in (§2).
- **Spawn targeting changed.** E-2125 made `spawn-window` target the SPAWNER's
  tmux session. A spawn fired from a monitor would land in the monitor's
  session, which is the out-of-sight failure E-1815 rejected. §5 adds an explicit
  target.
- **E-1993 (underway)** requires a plan to claim or spawn, parks tasks with
  open questions, and deletes the triage job. That job was the precedent for a Go
  job shelling out to the Python CLI (`childEnv`, `LookPath("endless")`).
- **E-1994** (triage-spawned primed sessions) overlaps, and its premise is being
  deleted by E-1993. Out of scope here; to be revisited on its own.
- **Background agents are gone** (E-2074), so references to `approve`
  refusing a background session, or to `SessionKindBackground`, no longer apply.
- The unverified pile is 40 (was 57), still sediment, so the cap still counts
  only auto-spawned work.

## 1. Eligibility

A task is eligible when ALL hold:

1. status = `ready`;
2. complexity = `low` and risk = `low` — hardcoded for v1. Configurable
   thresholds are deferred until there is experience with what low/low
   produces;
3. phase in (`now`, `urgent`);
4. its type is auto-spawnable (§3);
5. its project has opted in (§2);
6. not blocked by any non-terminal task (the `task next` rule);
7. has a `plan` content row and no `open` `task_questions` row — the E-1993
   spawn gates, pre-filtered so the selector never picks a task spawn would
   refuse;
8. never claimed by any session (spawn's `_check_prior_claim` refusal,
   pre-filtered for the same reason).

Conditions 6–8 mirror spawn's own refusals. A test asserts every selected
candidate is one `task spawn` accepts. Otherwise the selector would pick the
same unspawnable task on every run.

## 2. Configuration

- **Project** (`.endless/config.json`), because opting in is a decision about
  that project:
  - `auto_spawn.enabled` — default `false`. It is the kill switch, and it also
    scopes the database-wide job to opted-in projects.
  - `auto_spawn.cap` — default `3`.
- **User** (user config), because there is one selector job for all projects:
  - `auto_spawn.interval` — default `5m`. `Schedule()` reads it, so the rate is
    configurable with no new jobs machinery.
  - `auto_spawn.target` — `active` (default) or `monitor` (§5).

## 3. Auto-spawnable is a property of the task type

- `TaskType.AutoSpawnable() bool` in `internal/tasktype`: `todo` and `bugfix`
  are true, the rest false. E-2147's `docs` type sets its own when it lands.
- A matching `task_types.auto_spawnable INTEGER NOT NULL DEFAULT 0` column: a
  migration adds it, `seeds.sql` / `schema.sql` upsert it, and
  `tasktype.VerifyIntegrity` checks it against the method, so the table cannot
  drift.
- The selector filters on the column. No type is ever listed in selector code.

## 4. The cap

At most `auto_spawn.cap` auto-spawned tasks per project may be outstanding. A
task is outstanding while its status is `underway` or `unverified` AND the
session that claimed it has `auto_spawned = 1`. A stalled session counts
(E-1815). A task stops counting when it is confirmed, assumed, abandoned, or
sent back to `revisit`. A project at its cap is skipped for that run.

## 5. The spawn

- **Selector job** — a new registered job on the E-698 runner (for example
  `internal/autospawnjob`, `JobName = "auto-spawn"`). Each due run picks at most
  ONE eligible task across all opted-in projects under their caps: `urgent`
  before `now`, then oldest first. Rate = one task per interval (E-1815's
  throttle).
- **How it spawns** — shells `endless task spawn E-N --auto` through the same
  seam the triage job used (`LookPath` plus a `childEnv` pinning
  `XDG_CONFIG_HOME` to the runner's resolved config dir, so the child opens the
  same database). E-1993 deletes the triage job, so move that helper into a
  shared place (e.g. `internal/jobs`) rather than copying it.
- **`task spawn --auto`** (hidden flag) passes three things to `spawn-window`:
  - `-d` on `new-window`, so an auto-spawn never takes focus. Manual `task
    spawn` is unchanged.
  - An explicit target session. With `active`, the session of the most recently
    active attached tmux client (`list-clients`); with `monitor`, the spawner's
    own session, today's E-2125 behavior. If no client is attached, `active`
    resolves to nothing: the job skips and records why, so auto-spawn pauses
    while you are fully detached.
  - An `@endless_auto_spawned=1` window option, set in-process before exec
    alongside the existing `@endless_*` options.
- **Provenance** — `sessions.auto_spawned INTEGER NOT NULL DEFAULT 0` (a
  migration), written by SessionStart when it binds a session whose window
  carries the option. `spawned_by` stays null (E-1815). The flag goes on the
  durable `sessions` row, which E-2063's session_instances rework keeps. The
  session status / project monitor rows mark auto-spawned sessions with an icon.
- **Skips are not failures.** "No client attached", "every opted-in project at
  cap" and "nothing eligible" each end the run successfully, with the reason
  recorded where `endless jobs list` shows it: a nullable `jobs.last_note`
  column, set on every run and cleared by the next. Only a real error (the spawn
  subprocess failed) counts as a failure and backs off.
- The spawned session gets the same handoff template as a manual spawn, and ends
  at `unverified` like any other. Auto-verify, auto-land and stall detection stay
  downstream (E-1815).

## 6. Out of scope

- Configurable thresholds; a machine-wide cap (E-1815 deferred it).
- E-1994's primed / stop-and-ask sessions.
- Draining the unverified sediment (E-1977); rating the 36 already-`ready`
  tasks.
- `-d` for manual `task spawn`.

## 7. Verification

- Go unit tests: the eligibility query against a seeded DB, one case per
  condition in §1; the cap count; ordering and one-per-run; every skip reason;
  and `AutoSpawnable` / `VerifyIntegrity` drift.
- A property test: every candidate the selector returns passes `task spawn`'s
  own checks.
- `spawn-window` argv builders: `-d` present only with `--auto`; the target
  resolution; the option set.
- An end-to-end verify suite with a fake `tmux` on PATH, recording its argv: an
  opted-in project with one eligible task spawns exactly once per due run,
  detached, into the resolved target, with the auto-spawned flag bound; an
  opted-out project and a project at its cap spawn nothing and say why.
