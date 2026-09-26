# Automated dispute resolution between sessions

Absorbs E-1948 (routing reads as the agent's job) and E-1970 (surface the
triager's decision). Depends on E-1994 — primed sessions are the transport.

## The problem, precisely

Today the user is the message bus, and it is the worst possible allocation:

> filer files a plan → triage or the implementer objects → **the user** carries
> the objection to the filer → filer concurs or rebuts → **the user** carries
> the rebuttal back → repeat two or three times

Every round costs the user's attention, and they are adjudicating between two
agents where the **filing session holds context neither the user nor the
objector has**. The person with the least context is doing the routing.

This has a known workaround the user performs by hand — asking the implementer
to "write a prompt I can give to the filer with your concerns, so it can either
concur or deny and explain why" — which is exactly the protocol below, executed
manually. Automate what is already being done.

## The protocol

1. The spawned session finds the plan insufficient or wrong.
2. It **sends the filing session a message** saying so and why. (Step 2 of the
   lifecycle — the filing session is warm by construction here.)
3. Filing session **agrees** → the plan is updated. User never involved. **This
   is the majority case and the entire win.**
4. Filing session **disagrees and explains why** → the spawned session accepts
   (done, user never involved) or holds.
5. Neither moves → **write a `task_questions` row addressed to the user**,
   carrying both positions, and park the task.

No new machinery in step 5: escalation and open question are the same object.
The task parks exactly as it would for any unanswered question (E-1993 §3).

Step 5's shape matters: the user's original complaint was that agents decide
open questions instead of presenting pros and cons. This protocol produces the
pros-and-cons format as a byproduct — by the time anything reaches the user, two
agents have argued it on the record.

The filer is the right recipient because it is not deciding unilaterally; it is
**answering a challenge on the record**. That is the same adversarial principle
the rest of the epic runs on.

## Two things that must be built in from the start

**Preference questions skip step 2 entirely.** Some questions no amount of
filing-session context can answer — "60 characters or 100", "mechanical or judged". These
go straight to the user. Routing them to the filer burns a round-trip to produce
a guess. Misclassifying costs one round, so a cheap classifier is fine.

**Bound the loop.** A hard round cap before escalation. Two agents disputing
with no human in the path will otherwise consume tokens indefinitely and never
surface — an observed failure mode in multi-agent chat systems, not a
hypothetical. Trivial now, expensive to discover in production.

## Re-approval

When dispute resolution updates a plan, the user must get to review and
re-approve. E-1993's retargeted reset gives this for free: a material plan
change on a pre-work task drops approval. Verify it fires on agent-initiated
plan edits, not only user-initiated ones.

## Messages are notification, not record

Questions, objections, rebuttals and verdicts persist to the DB and ledger
regardless. The message channel exists to **get answers back** from a live
session, not to store anything. A dispute must be fully reconstructable from
the ledger after every participating session is gone.

## Transport

`SendMessage` over Claude Code's cross-session channel (v2.1.224+). Same-machine
delivery is a per-session Unix socket, never through Anthropic servers; worktrees
on one filesystem reach each other fine. Reachability requires the target session
to be **live and binding an inbox socket**, which is what E-1994's
primed-sessions-stay-alive decision provides. Delivered messages count as prompts
against usage, and an idle session starts a new turn on receipt — so the round cap
is a cost control, not only a correctness control.

See `docs/private/research-2026-08-14-cross-session-messaging.md`.

## Absorbed from E-1948

E-1948's substance survives and belongs here, since routing is what this task
automates:

- Accept `submitted` in `task update --status` (`task submit` stays as the
  standalone verb). The one-call path currently exists for "needs a plan" but
  not for "the description is sufficient", making the right answer the harder
  one to reach.
- Reword the transition message to lead with the decision the agent should make,
  naming the background sweep as a worst-case fallback rather than the mechanism.
- Drop the automation mechanics from CLAUDE.md — agents need the duty, not the
  machinery. Documenting triage as automatic reads as exclusivity and invites
  deferral.

## Absorbed from E-1970

The `triage_report` row and its rendering land in E-1994. What lands here is
the notification half: a triage verdict that changes nothing must still be
visible and must still reach someone. E-1970's live misdiagnosis on 2026-08-08 —
three tasks correctly triaged back to `unplanned`, reported as the feature not
working because the end state matched the start state — is the regression case.

## Acceptance

- An objection routed to a live filing session produces a concur-or-rebut
  response without user involvement.
- A concurrence updates the plan and drops approval.
- A deadlock escalates to the user carrying both positions.
- A preference-class question bypasses the filer entirely.
- The round cap is enforced and escalation on cap-hit is visible, not silent.
- A dispute is fully reconstructable from the ledger with every session gone.
- A dead filing session degrades to direct user escalation, not a hang.
- `task update --status submitted` is accepted.
- `go build/vet/test ./...` and `just test` pass.

## A third agent may reframe, never rule — decided

Before a deadlock reaches the user, a headless model call may read both
positions and turn them into a crisp decision with named options and their
pros and cons. It may **not** pick one.

The distinction is the whole point. An arbiter that rules re-introduces an agent
deciding an open question, which is the behaviour this epic exists to remove. An
arbiter that only reframes is doing formatting — and presenting a decision as
options with pros and cons is precisely what the user asked for at the start of
E-1989. Runs the same way as the challenge facet: a one-shot call inside the
existing job, no window, no session row.

## Step-7 disputes are allowed — decided

A session resumed later to do the work may also find the plan wrong, and may
also say so. Suppressing that does not remove the problem, it only leaves it
unaddressed.

They will genuinely arise, for three structural reasons rather than incidental
ones. The codebase drifts between the read-in and the work (E-1934 mitigates by
keeping durable content free of line numbers, but cannot eliminate it). Reading
a plan is not executing it — some conflicts only appear at the keyboard. And an
answer the user gave at step 5 can turn out to be unimplementable, which is a
dispute against the user's own decision and is exactly the kind that must not be
suppressed.

**But the counterparty is usually different, and that keeps the cost near zero.**
At step 7 the disputing session is the same session that read the plan at step 2
and may have amended it itself. So it is rarely disputing someone else's work —
it is reporting that a plan it accepted does not survive contact. There is often
no peer to message; the right move is to write a `task_questions` row and park.

That machinery already exists for every other question. So: allow step-7
disputes, and implement only the escalate-to-user path. **The peer-message path
at step 7 is the part that may be YAGNI** — build it if step-7 disputes turn out
to have a live counterparty worth messaging, not before.

This also formalises something sessions already do: stopping to ask. The gain is
that the question becomes a durable row instead of a chat message that scrolls
away.


