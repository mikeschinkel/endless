Resolution of the open question the description carried, decided by Mike
2026-08-25: the new status is `unreviewed`. Everything below follows from that
and is settled — nothing here is left for the implementor to re-decide.

WHY DISTINCT, NOT A REUSE OF `unverified`

`unverified` means "implementation done, awaiting verification", and
research/epic/brainstorm are explicitly REFUSED it — E-1577/E-1579/ED-1516 gate
it as a type-correctness invariant with no --force bypass, on the grounds that
those types produce information rather than testable behavior and terminate via
`completed --outcome`. That gate now lives in taskstatus.VerificationTrack.
Reusing the lane would collapse a distinction three tasks drew on purpose.

`unreviewed` says something different, and the difference is the point: not
"does it work" but "has the owner read it". E-1817 failed the second, never the
first.

WHY ONE STATUS FOR BOTH TYPES

Brainstorm and research differ in deliverable — a synthesis versus findings —
but not in the failure mode. Both finish on the author's say-so with nobody
downstream. One name, one gate. If they ever need to diverge, that is a
membership change in one map, not a second status.

IT BLOCKS DEPENDENTS

Same reasoning as `unverified`, and stronger here. `unverified` blocks because
"implementation done" is not "trusted". `unreviewed` is that shape one track
over — and the deliverable of research and brainstorm is INFORMATION OTHER TASKS
CONSUME, so an unreviewed outcome is more dangerous downstream than unverified
code, not less. E-1817 is the proof: its outcome changed materially through five
rounds of correction after the session declared it done. Anything that had
started on the self-declared version would have built on a deliverable that
moved five times.

Mechanically this means `unreviewed` is NOT in taskstatus.Terminal, which is the
set that releases a dependent as of E-1891.

GROUP MEMBERSHIP

It mirrors `unverified` almost exactly. In: NotActionable (it awaits someone
else's read, so it is not pickup-able), Active (`task active` is "in flight,
yours to push"), ChildrenStateOrder (its own bucket — non-terminal), Settled
(claiming needs --force; the tier clears), Shipped (`obsolete` is refused — the
work happened). Out: Terminal, Actionable, ClaimPromotes, DerivationPrecedence,
DescriptionResetFrom, Open, PreJudgment, Reopenable, ReopenRefused,
SetsCompletedAt, StickyOverride, SubmittableFrom.

Two that need more than a membership edit:

  - The session-status dispositions are Terminal, `blocked`, `unverified` and
    SessionPending, and the middle two are bare status comparisons rather than
    groups. `unreviewed` belongs with `unverified` — both mean "awaiting
    someone's sign-off" — so that bare check becomes a two-member group. The
    partition test will fail until it does, which is the design working.

  - _TYPE_FORBIDDEN_STATUSES gains the inverse of the gate that motivated this:
    todo and bugfix must be refused `unreviewed`, exactly as research, epic and
    brainstorm are refused the VerificationTrack. Without it the two tracks are
    only half-separated.

EXISTING ROWS ARE LEFT ALONE

No backfill. Research and brainstorm tasks already sitting at `completed`
shipped under the old rule; retroactively demoting them would assert that a
review did or did not happen, and nobody knows which. The gate applies going
forward.

WHAT IT COSTS NOW THAT E-1891 HAS LANDED

This is the first status added since the registry exists, so it is also the
first real test of whether E-1891 did its job. Add the const to
internal/taskstatus and the single `groups` map forces a decision on every
grouping rather than letting one be missed — the failure that hit E-1648 and
E-1845. Three partition invariants fail until those decisions are made
(actionable|not-actionable, children-state-order|terminal, and the
session-status dispositions), plus the label/glyph and membership-pinning tests.
`unreviewed` needs a glyph; ☑ is taken by `unverified`, and the adjacent
unclaimed shape is ☐. Python needs no vocabulary change at all: statuses.py
holds nothing and reads whatever Go says.

SEQUENCING WITH E-2018

E-2018 builds transition-legality enforcement on the same registry. If it lands
first, `unreviewed` adds edges (underway → unreviewed → completed) to an
existing graph instead of arriving with no enforcement to hook into. The
existing blocked_by already says so.
