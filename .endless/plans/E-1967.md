# Plan: refuse spawn on a task a prior session already claimed

The guard needs a durable answer to "has any session ever claimed this task?"
As of E-1968 that answer already exists in `sessions.task_id`, so this task is
the guard plus a label fix plus a small repair of pre-E-1968 damage — not the
schema work the 2026-08-13 plan called for.

Mike's decisions, 2026-08-24 unless noted. Message strings marked DRAFT are mine
to propose and his to edit.

## What changed since the 2026-08-13 plan

That plan keyed the guard on `session_tasks` because the session-binding
column (then named `active_task_id`, now `sessions.task_id`) could not be
trusted: `task reopen` NULLed it silently, which is how E-1859's
link to ES-1046 was lost. Three things have since removed that premise.

- **ED-1560 (accepted)** — `sessions.task_id` is write-once: set at claim, never
  cleared, never repointed. It IS the ownership record.
- **E-1968 (landed)** — removed every writer that cleared it: `task reopen`'s
  release, `monitor.CompleteTask`, `StartChatSession`'s ON CONFLICT, and
  `task release`, which is now disabled outright.
- **E-1969 (landed, e9834c0)** — renamed the column to `sessions.task_id` (and
  `active_epic_id` to `epic_id`) and installed `sessions_task_id_write_once`, a
  `BEFORE UPDATE` trigger that aborts any change to a non-NULL value.
  Re-affirming the same value is allowed, and so is `NULL → value`; only
  changing or clearing a set binding aborts.

The column carries no `UNIQUE` constraint and E-1969 adds none — the trigger
constrains a row's history, not how many rows share a value. Many sessions
already point at one task in live data (E-1857 and E-1307 have seven each). So

    SELECT id FROM sessions WHERE task_id = ?

is the complete claimant history, ended sessions included. That is exactly what
the guard needs, and it needs no schema change to get it.

**Superseded by the above:** the old plan's Decision 2 (widening
`session_tasks`'s UNIQUE key to `(session_id, task_id, relation_id)` so claim
appends a row, plus the audit of every reader that assumed one row per pair) and
Decision 3's `session_tasks` backfill. Neither is built here. What survives of
Decision 3 is a much smaller repair — see §4.

**Also dead in the old plan, and not to be resurrected:** its escape hatch
(`endless task release E-1859` then `task spawn E-1859`) named a command E-1968
disabled; its claim that `--new-session` "moves to the navigation verbs" is
wrong, since E-1968 dropped that flag entirely; and its verification bullet "the
same spawn with `--new-session` proceeds" contradicted its own "no escape hatch"
line even before that.

## Division of labour between the two tables

State this in the code, because the whole task turns on it:

- **`sessions.task_id` is OWNERSHIP.** Write-once, authoritative, and the only
  thing the guard consults.
- **`session_tasks` is INVOLVEMENT.** Which tasks a session touched and how it
  first touched them. Its `relation_id` is stamped at first touch and never
  revised, so a session that files a task and later claims it reads `Surfaced`
  forever. That imperfection is left in place: it is a record of first contact,
  and §3 stops it being mistaken for an ownership claim.

## 1. Rename the relation `goal` → `claimed`

Carried forward unchanged from the 2026-08-13 plan. `Goal:` cannot be reasoned
about; `Claimed:` says what happened. Rename the slug and the label together,
here rather than in a follow-up.

Touches the `session_task_relations` seed rows, every reader matching on the
slug, and the "Touched by:" render. Historical rows keep their id, so this is a
label change, not a data migration.

The row is still written on claim exactly as today, under the existing
`UNIQUE(session_id, task_id)`. A session that already has a `surfaced` or
`revisited` row for the task keeps it — that is the frozen-tag behavior above,
and §3 is what makes the rendering honest anyway.

## 2. The guard

In `task spawn`, after the existing live-owner check (`_check_task_ownership`,
which passes `current_eid=None` and raises on a *live* owner), add:

> Refuse when any session row — **any state, including `ended`** — has
> `task_id = <target>`.

Ordering matters: the live-owner check runs first so "someone is working this
right now" keeps its own, more specific message. The new guard covers the case
that check misses, which is the whole defect: the prior session is usually
`ended`.

No exclusion for the spawning session. Spawn is run from a session working a
different task, so the spawner is never the claimant, and the codebase already
settles this — the existing guard passes `current_eid=None` with the comment
"treats any owner as 'other' — spawn never claims ownership for the spawning
session." The exclusion belongs to `claim`, where re-claiming your own task is
legitimate.

The one case an exclusion would have covered — the spawning session claimed this
task earlier — cannot arise under ED-1560: a session holds one task for its
lifetime, so a session spawning onto a different task never claimed the target.

**No escape hatch, and now genuinely none to offer.** `--force` is not added,
`--new-session` is gone, and `task release` is disabled — so unlike the
2026-08-13 plan there is no two-command workaround to document. If you want to
work a task a prior session claimed, you resume that session. That is the point.

DRAFT refusal:

    E-1859 was claimed by session ES-1046. Pick the work back up there:
        endless session goto E-1859 --resume --revisit
      (--no-revisit instead, to read it back without reopening the task.)

    Spawning a second session on it would start over without ES-1046's
    reasoning, which is only in that session.

With more than one claimant, name the most recent in the command and list the
rest beneath. `--revisit` / `--no-revisit` are E-1968's flags and are required
only when the task is `confirmed`/`assumed`/`completed`; render the command with
them only in that case, or the message teaches a flag the user will be told is
invalid.

Match the shape of E-1968's own refusal on settled work, which this now sits
beside in the same command.

## 3. Render `Claimed:` from `sessions`, not from `session_tasks`

`task show`'s "Touched by:" block reads `session_tasks`, so it shows ES-1046 as
`Revisited` on E-1859 even though that session owned the task — the defect that
made "which sessions claimed this" unanswerable.

Fix it in the render, not the schema: a session whose `sessions.task_id` matches
the task renders `Claimed:`, whatever `session_tasks` says for that pair.
Sessions present only in `session_tasks` render with their existing label.

This makes the render agree with the guard by construction — both read the same
column — and removes the reason Decision 2 existed.

## 4. Repair the pre-E-1968 damage

The guard protects only tasks whose claimant still has a binding. Twenty-one
`task.released` entries exist in the whole ledger, so at most twenty-one
bindings were ever destroyed — E-1859's and E-1917's among them. Those are
exactly the old tasks most likely to be wrongly spawned onto, so repair them.

**A one-time migration is the only route, and E-1969 established that it is a
safe one.** Two facts, both from E-1969's schema commentary and confirmed
against `internal/events/projector.go`:

1. **`sessions` is not projected from the ledger.** The projector has cases for
   task and decision events only and skips everything else — there is no case
   for `task.claimed` or `task.released`. So `rebuild-db` never touches session
   bindings: it will not undo this repair, and it will not perform it either.
2. **The trigger cannot fire on this write.** It aborts only when
   `OLD.task_id IS NOT NULL`. Restoring a destroyed binding is `NULL → value`,
   which is permitted, so no exemption is needed.

Write it as an `internal/schema/changes/e-1967-*.go` change file, following the
existing pattern: for each session with `task_id IS NULL` that has a
`task.claimed` ledger entry naming it, set the binding. The ledger is the only
surviving evidence of these claims, since the `sessions` table is machine-local
runtime state rather than a projection. Skip anything ambiguous rather than
guessing; a task that falls back to today's behavior is not a regression.

**Do not neuter `execTaskReleased`, and do not reason about it from a rebuild.**
An earlier revision of this plan proposed that, on the belief that a rebuild
replays `task.released` and would re-destroy the repaired bindings — or abort on
the trigger. Both are false, per fact 1. The executor is unreachable in practice
and is best left exactly as it is.

**Correct one stale comment while here.** `execTaskReleased`'s doc comment
(written by E-1968, and the source of the wrong reasoning above) still claims
historical `task.released` entries "must still replay to reproduce the state
they produced" and that "a rebuild will abort on the first one". Both are wrong,
and E-1969's schema comment already states the truth a few files away. Two
comments in the tree contradict each other; delete the wrong one. This is a
comment fix, not a behavior change.

## 5. What this task does NOT do

`session goto E-NNNN --resume` resolves through `sessions.task_id`, so once §4
restores the destroyed bindings, task→session resolution works for every session
that ever claimed a task. No `session_tasks` fallback is needed, and none is
built. Do not claim `goto --resume` is broken for ended sessions — it is not;
its resolver never filtered on state.

## Sequencing

Unblocked. **E-1968** (assumed, f8a6148) supplies the write-once column this
guard reads; **E-1969** (assumed, e9834c0) supplies its name and settled §4's
open question. This plan is written against post-E-1969 identifiers —
`sessions.task_id`, `sessions.epic_id` — so read the code, not this plan, if a
name looks unfamiliar.

## Verification

- A task whose claimant is `ended` refuses a plain spawn and names that session.
- The refusal names `session goto <id> --resume`, and includes
  `--revisit`/`--no-revisit` only when the task is confirmed/assumed/completed.
- Several claimants: the most recent is in the command, the rest are listed.
- A task no session ever claimed spawns unchanged.
- A LIVE owner still gets the existing live-owner refusal, not the new one.
- `task show` renders `Claimed:` for a session holding the task, including one
  whose `session_tasks` row says `surfaced` or `revisited`.
- `task show` renders `Claimed:` where it previously rendered `Goal:`.
- Post-repair: E-1859 refuses a plain spawn naming ES-1046, and
  `session goto E-1859 --resume` finds ES-1046 despite it being ended.
- The repair survives `endless-go event rebuild-db` — which it must, trivially,
  since the projector has no session cases at all. Assert it anyway: it is the
  fact the previous revision of this plan got wrong.
