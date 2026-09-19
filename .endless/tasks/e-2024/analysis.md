## Supersedes this task's original framing

E-2024 was first filed as "refuse `--keep-status` from an agent." That was wrong.
The flag was written FOR agents, to assert in their judgment that an edit is not
a re-spec. Removing a deliberate capability because the caller keeps narrating
its use is fixing the wrong thing.

## What the code actually does today

`update_plan` infers four transitions:

| transition | decided by |
|---|---|
| plan-attach promotes a pre-work task to `submitted` | deterministic — text is non-empty |
| `--tier 1` advances a pre-work task to `ready` | deterministic |
| a **material** description edit resets to `untriaged` | `description != row["description"]` |
| editing plan text of a done task flips to `revisit` | `text != row["text"]` |

The bottom two are documented as judgments and implemented as byte inequality.
Any change at all fires them; only a byte-identical rewrite does not. That gap is
what `--keep-status` exists to paper over: a typo fix and a re-spec are
indistinguishable to `!=`, so a human or agent has to assert the difference by
hand on every edit that might be either.

## The proposal

Do not decide the bottom two inline. Enqueue them, and let a detached `claude -p`
judge them with both versions of the field in hand — which is the only place the
question "is this a re-spec or a typo?" can actually be answered.

The top two stay synchronous. They are deterministic, there is nothing to judge,
and deferring the plan-attach promotion would be actively harmful: that promotion
is how an agent signals "spec-complete, awaiting your approval", so latency there
delays the human's `task approve`.

Precedent is exact rather than analogous: `endless task add` already spawns the
triage of one task detached, with `endless triage run` sweeping as the backstop,
and fails open by leaving the task alone. Same shape, same failure mode, an
existing job registry to hang it on.

## Agreed constraints (confirmed with Mike)

**1. Only the two judgment-shaped transitions are queued.** Plan-attach promotion
and tier-1 advance stay synchronous: they are deterministic, and deferring
plan-attach would delay the very signal that asks the human for approval.

**2. The enqueue is silent to the caller.** No stdout, no stderr, nothing in
`--json`. This is load-bearing, not polish. If `task update` announces "queued a
status review", an agent relays that line and the problem is exactly where it
started — the noise never depended on the decision existing, only on there being
something subtle to have noticed.

Empirical, not hypothetical: Mike reports having seen an agent write "the status
changed on its own" 100+ times already. Removing the decision does not remove the
narration unless the event is also invisible to the actor.

**3. Frequency is bounded and measured.** Triage runs once per task creation;
this would run per edit. Enqueue only when the byte-compare already says
something changed AND the current status is in `_DESCRIPTION_RESET_FROM` /
`_REOPENABLE_TERMINAL_STATUSES` — already the rare case. Measure before assuming
it is cheap.

## Open questions for pickup

- Eventual consistency. Status is briefly stale after an edit. Nothing in the
  synchronous path appears to read it back, but confirm before landing.
- Does `--keep-status` survive? If the judge answers the question the flag was
  invented to answer by hand, the flag becomes redundant rather than forbidden —
  and redundant is the only good reason to remove it. Decide after the judge
  works, not before.

## Honest caveat

Part of the motivation is behavioral: an agent that has no decision to make has
nothing to justify, and the inline decision has been generating unrequested
"here is why I did or did not suppress the transition" reporting. That alone
would not justify this much machinery. What justifies it is that the judgment is
real, is currently not being made, and is being substituted for by a manual flag
on every call.

## PRODUCT

Every project using Endless gets these transitions and this flag, so every
project's agents hit the same inline decision. The judge is a detached process
like triage, so it needs no tmux and no worktree; `self_dev` changes nothing.
Projects without an available `claude -p` must fail open to today's byte-compare
behavior rather than blocking the edit.
