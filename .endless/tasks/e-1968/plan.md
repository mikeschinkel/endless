# Plan: make the session/task movement verbs account for task state

One root: the verbs that move you between sessions and tasks act on task state
without making it explicit. `task reopen` destroys a binding silently,
`session resume` clobbers a pane that may hold live work, `session goto
--resume` cannot express intent about a task needing revisit, and `task spawn
--reopen` exists only because none of the others could change status.

All decisions are Mike's, 2026-08-13, except strings marked DRAFT — mine to
propose, his to edit.

## 1. `task reopen` gates its release behind `--force`, and announces it

`_reopen_task_core` releases the session→task binding — sets
`sessions.active_task_id` to NULL for whichever session held the task — as a
deliberate side effect, and prints nothing. Its `--help` says "Metadata-only:
no worktree creation, no session binding," which reads as "does not create
one" and is silent about destroying one.

Without `--force`, refuse when a binding exists. DRAFT:

    E-1859 is bound to session ES-1046. Reopening would release that binding,
    and `endless session goto E-1859 --resume` would no longer find it.
    Pass --force to reopen and release, or run
    `endless session goto E-1859 --resume` to pick the work back up.

With `--force`, proceed AND print one line naming what was released. The
announcement is not suppressible and is not verbose-only; a `--force` that
still hides the effect is the same defect with an extra keystroke. Reopening a
task with no binding is unaffected — no gate, no message.

Fix `task reopen --help` to state what it does to an existing binding.

## 2. `session resume` requires `--force` when the current pane holds a task

`session resume` relaunches into the CURRENT pane, clobbering it. When that
pane's session has a task bound, that is destroying live context. Require
`--force`, and name the task in the refusal. DRAFT:

    This pane is working E-1958. `session resume` would replace it.
    Use `endless session goto <ref> --resume` to open the target in a new
    window instead, or pass --force to replace this pane.

Note `session goto --resume` already exists precisely to avoid the clobber, so
the refusal has a good answer to point at.

## 3. `session goto --resume` gains `--revisit` / `--no-revisit`

When the target task is in a state needing revisit, require one of the two and
error explaining both:

- `--revisit` — flip the task to `revisit` and open the session to continue work.
- `--no-revisit` — open the session read-only, to page back through it with no
  intent to change anything. Task status untouched.

DRAFT:

    E-1859 is 'assumed'. Say what you intend:
      --revisit      reopen it and continue work
      --no-revisit   just read the session; leave the status alone

Neither flag is required when the task is in a state that needs no revisit.

## 4. Retire `task spawn --reopen`

Its only unique capability is changing task status, which item 3 now provides
on the navigation verb. Retiring it removes the composite verb and leaves three
single-purpose ones — which is also what makes a future top-level `endless
goto` / `endless resume` / `endless reopen` map cleanly, with no composite to
place.

Decide during implementation whether to delete outright or deprecate with a
message pointing at `session goto --resume --revisit`, given there are no
external users.

`--new-session` moves to `session goto --resume` / `session resume`, NOT to
plain `spawn`. Mike's call, and the reason is that on `spawn` the flag is
tautological — spawning always starts a new session, so "start a fresh session
instead of inheriting" only means something on a verb whose default IS to
inherit. That is the navigation verbs.

Consequence to resolve: E-1967's spawn guard currently names `--new-session` as
the way to spawn anyway on a task a prior session claimed. With the flag gone
from `spawn`, that guard has no escape hatch. See the open question in E-1967.

## 5. `task release` moves an `underway` task to `revisit`

Today a bare release leaves the task `underway` with no session bound — a status
that means "a session is working this" while nothing is. That inconsistent state
is what made E-1859 hard to find.

`underway` is the ONLY status a release touches. Every other status a bound task
can hold is true independent of the binding: `unverified` ("implementation done,
awaiting verification"), `revisit`, the terminal statuses, and the pre-work
statuses a task can carry when bound via `task bind` rather than claimed. None
of them lie when unbound, so none of them move.

It moves to `revisit`, NOT to `ready`. `claim` promotes from five statuses —
`untriaged`, `unplanned`, `ready`, `blocked`, `revisit` — so "put it back" cannot
mean `ready` without granting approval to a task that may never have had it,
which is the one thing `ready` exists to prove. `revisit` is safe from all five:
it is never a stronger claim than the truth, and the way out
(`revisit → submitted → ready`) re-earns approval rather than assuming it. It is
also honest that a release may leave partial work in the worktree, so whether the
plan still holds is unknown.

Accepted cost: a release seconds after a claim, with nothing done, also lands
`revisit` and needs a re-submit and re-approval. The alternative — restoring the
pre-claim status from the ledger's status-change history — is accurate in every
case but is real machinery. Revisit that only if the friction bites.

Rejected: making `release` always hand the task to a new session so no unowned
state exists. Mike, on least surprise — "I asked it to release a task, NOT spawn
a new session." A release that spawns is a different verb wearing release's name.

## 6. Rewrite `task spawn`'s done-ish refusal

Raised when `--force` was not passed and status is in `_CLAIM_REQUIRES_FORCE`
(`unverified`, `confirmed`, `declined`, `obsolete`, `assumed`, `completed`).
For the subset also in `_REOPENABLE_TERMINAL_STATUSES` (`assumed`, `confirmed`,
`completed`) it currently reads:

    E-NNNN is '<status>'; pass --reopen to reopen-and-spawn, or run
    `endless task reopen E-NNNN` first.

Both branches are wrong once item 4 lands, and the second was always the worse
of the two: running `task reopen` first releases the binding AND moves the task
to `revisit`, outside `_CLAIM_REQUIRES_FORCE`, so the follow-up plain `spawn`
proceeds with no prompt at all. The message routed the user into the trap.

DRAFT replacement:

    E-1859 is 'assumed' and was claimed by session ES-1046. Pick it back up:
        endless session goto E-1859 --resume --revisit

The other branch — force-set statuses that are NOT reopenable — keeps its
existing "pass --force to confirm the demotion" wording.

## Sequencing

Item 6's message names the prior claimant, which comes from E-1967's `claimed`
rows. Land E-1967 first, or land item 6's message without the claimant clause
and add it after.

Items 1-5 are independent of E-1967.

## Verification

- `task reopen` on a bound task refuses and names the session; `--force`
  succeeds and prints the release; an unbound task is unchanged.
- `session resume` from a pane with a bound task refuses and names it;
  `--force` proceeds; from a pane with no bound task, unchanged.
- `session goto E-NNNN --resume` on a revisit-needing task refuses naming both
  flags; `--revisit` flips status and opens; `--no-revisit` opens without
  touching status.
- `task release` on an `underway` task leaves it `revisit`; on `unverified`,
  `revisit`, a terminal status, or a `task bind`-bound pre-work status, the
  status is untouched.
- `task spawn --reopen` is gone (or deprecated with the pointer).
- `task spawn --new-session` still works without `--reopen`.
- Spawn's done-ish refusal no longer mentions `task reopen`.
- `task reopen --help` describes the release.



---

## Addendum — the verbs under a write-once task_id (added from E-1952's session, 2026-08-15)

E-1969 makes `sessions.task_id` write-once (NULL → one value, never
reassigned) and enforces it with a `BEFORE UPDATE` trigger. Every verb here
that re-points a session must be reconciled with that, and **this task lands
with or before that trigger** — otherwise the re-pointing verbs start aborting
on contact.

**`bind` becomes first-set-only.** It may fill a session with no task. It may
no longer move a session from one task to another. Today its help says it
"just sets sessions.active_task_id"; under the invariant that is only legal
once.

**`release` is disabled, not deleted.** Clearing to NULL and then setting a new
value is reassignment through the back door, so release cannot survive as a
workflow. But delete it later, not now:

- Keep the command and its wiring. Make it exit with an error saying it has
  been deliberately disabled under the one-session-one-task invariant, and that
  re-enabling means removing the disabling code — to be done only if a real
  use-case appears that the invariant cannot serve.
- Comment the disabling code with the date it was disabled (2026-08-15) and
  that instruction, so the tombstone carries its own review criterion.
- If no use-case has appeared after several months, delete it.

**Why the tightening.** Endless was built before one-session-one-task was known
to be the right model, so these verbs still assume a session can move between
tasks. That mobility has caused problems with no benefit — see the E-1969
addendum for the incident (ES-1067 re-pointed five times, one reassignment with
no identifiable source).
