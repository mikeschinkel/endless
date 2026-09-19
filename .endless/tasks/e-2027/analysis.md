# Why this is separate from E-1696, and why it IS blocked

Split from E-1696 with Mike on 2026-08-21. E-1696 landed everything that needed
no new storage: the `referenced` + `queued` vocabulary, the upgrade-only
precedence ladder replacing E-1462's set-once rule, `session task add`/`remove`,
and the relation display tier. This is the auto-capture gate itself.

## Prerequisite status

- **Actor detection — SATISFIED.** E-1462's analysis named agent-vs-human
  detection as a prerequisite; E-1962/E-1966 landed `agent_env.present()` /
  `internal/agentenv`. The gate keys on it directly.
- **Machine-user ledger — E-1673.** A hard `blocked_by`. See below.

## The volume argument (measured 2026-08-21)

The committed ledger holds 8,027 events. The entire "muddy bucket" —
`session_status.*`, `focus.*`, `session_tasks.ordered`: committed but never
replayed — is 22 lines, 0.27%.

Read capture changes that. `task show` is step 1 of `endless guide`'s happy
path, and the session implementing E-1696 made nine `task show` calls before
writing a line of code. Against ~128 recorded `task.claimed` events that is
1000+ events — the 4th-largest kind in the ledger, larger than `task.landed`
(576) — growing without bound, every line saying "this developer glanced at
E-1234". 458 of the last 899 commits already touch the ledger.

Per Mike (2026-06-30, folded into E-1462's Extension): reads are session-local,
ephemeral and per-developer, so they go to a gitignored machine-user ledger, not
the shared one.

## Why blocked_by E-1673, and not the narrow workaround

This task was FIRST filed carrying its own narrow `local/` routing for its one
event kind, related to E-1673 rather than blocked by it, so it could land
without waiting. Mike corrected that on 2026-08-21 and the narrow routing is
removed. Recording why, because the reasoning generalizes:

Duplicating a blocker's small load-bearing piece into the blocked task is a real
technique — it is what unblocked E-1696, which needed nothing from E-1673 at all
and would otherwise have been parked behind an unapproved 8-workstream epic. But
it is a COST paid to buy a benefit, and the benefit is "this ships sooner". Here
there was no such need: nothing downstream is waiting on this task. Paying the
cost anyway was pure loss.

The cost was not hypothetical. E-1673's design explicitly forbids the shape the
workaround would have introduced:

> Scope must be declared, never defaulted. [...] Do not let "project is the root
> default" decay into "undeclared is fine."

A one-kind opt-in is a convention where E-1673 wants a gate — so the "narrow
subset E-1673 later subsumes" framing was too generous. It would have left a
half-gate for E-1673 to unpick, plus a second round of `.gitignore` and
`endless register` scaffolding.

**The unlock is E-1673's own approval, not a workaround.** E-1673 is `submitted`
with a plan marked "Design is locked; this plan is option-free", carries no
`blocked_by` of its own, and its parent epic E-1671 is a parent, not a gate. It
can be approved and landed on its own schedule. That makes `blocked_by` here
honest AND cheap: the blocker actually moves.

## Scope

1. Emit a new event kind for the read capture from the `task show` /
   `task detail` read path, gated on `agent_env.present()` and on a resolvable
   session. Declared `machine-user` scope, so E-1673's emit gate routes it to
   `.endless/db-ledger/local/` — this task adds a declaration, not a routing
   mechanism.
2. No new executor logic needed: `upsertSessionTask` already accepts
   `RelationReferenced`, and the ladder already handles read-before-claim
   (pinned by `TestSessionTasks_RelationUpgrades` — the referenced→goal case).
3. Revisit collapsing the referenced tier behind a flag once real volume exists.
   E-1696 shipped dimming plus a hard sink, which E-1462's Extension offers as
   the alternative to collapsing; whether a `… N referenced` footer is also
   wanted is a question only real read volume can answer.
