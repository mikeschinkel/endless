# Read-through and primed sessions

The judged half of E-1991, and the piece that turns triage from a router into
an evaluator. Absorbs E-1946 (type-aware routing). Depends on E-1531
(`task_content`), E-2176 (`task_questions`), E-1993 (the gate), and E-1813
(complexity and risk ratings).

**E-1813 is startable.** Its plan says "do not start until ED-1538/ED-1539 are
ratified" — both went `accepted` on 2026-08-14 and nothing noticed.

## 1. Triage is a job with facets; name them

"Triager" has been used for both the job and one facet of it, and that
ambiguity is why this task exists as a separate piece of work. The job is
`triage`. Its facets, provisionally:

| facet | question | cost |
|---|---|---|
| shape check | is the title a title, the description a description, is there a plan? | cheap, mechanical |
| read-through | where would an implementer stop? → open questions | expensive, the core |
| the challenge | what did this plan decide without noticing it was deciding? | expensive, adversarial |
| dispatch | who hears about it | cheap |

**Settle the names as the first act of this task**, before building. They are
provisional on purpose. In particular "read-through" was chosen when the facet
was understood as *simulating* an implementer; §2 collapses that, so the name
may no longer fit. Nothing is simulated — the session begins and stops.

## 2. The read-through session IS the implementing session

Rather than spawning a throwaway evaluator, triage spawns the **real
implementation session**, with a handoff that ends: read the plan and the
codebase, determine whether you could complete this task, write any open
questions to the `questions` slot, then **stop and wait**.

That session is **primed**: it has read in, recorded what it needs, and is
holding. When the user is ready, they resume it — context already warm, no
re-read.

This collapses two problems at once. There is no evaluator-vs-implementer gap to
calibrate, because they are the same session; a predicted "this plan is
sufficient" and an actual one are the same judgment by the same context. And the
open questions arrive **while the user's own context on that task is still
warm**, which is the real scarce resource — not calendar time.

The extra handoff clause applies only when **triage** spawned the session, never
when the user did.

`primed` needs to be a tracked, nameable session state. A primed session sitting
idle is otherwise indistinguishable from a hung one.

**Primed sessions stay alive.** They do not exit after the read-through. A live
session can receive messages; an exited one cannot, and E-1995's dispute
resolution runs over exactly that channel — so liveness is enabling
infrastructure, not an artifact. The cost is real (a live process and pane per
primed session; comfortable in the tens, painful in the low hundreds) and is
accepted for now. If it bites, the graceful fix is live-by-default with
least-recently-needed eviction and resume-from-transcript on demand: an evicted
session loses reachability but not context. Do not build eviction now.

## 3. Integrate with auto-spawn — one mechanism, two terminal instructions

E-1812/E-1814 already spawn tasks judged safe to run unattended, keyed on
E-1813's complexity and risk ratings. Priming spawns everything else. These are
not two systems:

> **Low complexity AND low risk AND phase in (`now`, `urgent`) run to
> completion. Everything else pauses for input from the user.**

Same spawn plumbing, same read-through, eligibility chooses the terminal
instruction. Consume E-1814's eligibility computation rather than
reimplementing it.

**Phase is a required third condition, not a tiebreaker.** Complexity and risk
answer "is this safe to run unattended"; they say nothing about "does the user
want this done now." Without the phase condition, a `later` or `maybe` task that
happens to be simple and low-risk would run itself — spending tokens and
producing a branch to review for work that was explicitly deferred. `maybe` is
worse still: it means *may or may not be done at all*, so auto-completing one
decides a question the user reserved.

E-1814 owns the eligibility computation and its description now carries this
condition. ED-1538 defines eligibility as "computed from human-ratified
complexity+risk" and arguably needs amending to name phase as well — flagged for
the user, not assumed.

Start conservative — a narrow run-to-completion band. Widening it later is a
threshold change, not new machinery.

## 4. Type-aware evaluation (absorbs E-1946)

The sufficiency question is one sentence and it is the same for every task type:

> **Is this plan sufficient, without open questions, to allow this task to be
> performed?**

What counts as sufficient is type-dependent. For a brainstorm the plan is the
framing to be explored, so "no implementation steps" can never mean "needs a
plan first" — E-1946's bug. For research it is the request. For a do-task it is
the approach. Same question, type-aware standard.

Acceptance criteria (an `acceptance` row in E-1531's `task_content`) are an input here: the
read-through cannot judge sufficiency without knowing what done looks like.

## 5. Triage writes a durable report

Every triage run writes a `triage_report` content row: the verdict, the
rationale, the deciding model, and when. `task show` renders it.

This is what makes a no-op routing visible — a task triaged and judged
unspawnable currently looks identical to one never triaged, which caused a live
misdiagnosis on 2026-08-08 (see E-1970, absorbed into E-1995's scope for the
messaging half; the report itself lands here).

## 6. Eager for new, lazy for grandfathered

New tasks: read-through fires shortly after a plan is attached, while the filing
session is still alive — which is what makes E-1995 possible at all.

Grandfathered tasks: fired by E-1993's spawn gate, at the moment of need.

## Acceptance

- Facet names are settled and used consistently in code, docs, and output.
- Triage spawns a real implementation session that reads in, records questions,
  and holds in a queryable `primed` state.
- A primed session is reachable by message while holding.
- Resuming a primed task uses that session; it does not re-read from cold.
- A low-complexity low-risk task runs to completion without pausing; anything
  else pauses.
- A brainstorm with a sufficient framing and no implementation steps is judged
  spawnable (E-1946's regression).
- Every triage run leaves a `triage_report` row and `task show` renders it.
- A grandfathered task with no plan triggers a read-through on first spawn
  attempt rather than a bare refusal.
- `go build/vet/test ./...` and `just test` pass.

## The plan is never changed underneath the session — resolved

An earlier draft treated "the plan changed while the session was paused" as the
main correctness risk. It is not a risk, because **the paused session is the
thing that updates the plan.** The lifecycle:

1. Session spawned immediately after filing; filing session still warm.
2. It reads in, writes its questions to `task_questions`, pauses.
3. A separate process notices open questions and gets the user's attention
   (E-1996 covers that UI; E-1976 may be the same surface).
4. The user answers.
5. The session is pinged, reads the answers, **updates its own plan**, and waits.
6. The user returns and the work proceeds.

Nothing external rewrites the plan behind the session's back, so there is no
stale plan to reconcile.

E-1917 already landed the notification half of step 5 — it re-asserts a held
task's current state in the per-prompt injection with a sticky
"changed since you last read it" marker, suppressed when the session itself made
the change. **Retarget that marker from description to plan**, matching E-1993
§4. Do not build a second notification path.

**The residual is codebase drift, not plan drift.** Between step 2 and step 6 —
possibly weeks — the code moves while the session's understanding of it does
not. Cheaply handled: at step 6, re-check whether what the plan cites still
exists before proceeding. E-1934 (underway) reduces the exposure by keeping
line numbers and other time-frozen specifics out of durable task content, so a
plan cites names that survive drift.

## The read-through may draft a plan, gated by the challenge facet — resolved

A planless task is exactly where a read-through is most useful, since it can
draft the plan rather than merely refuse. Drafting is allowed, with one
condition: **a drafted plan must pass the challenge facet before it can reach
`submitted`.**

Without that condition the same agent authors and executes with only the user's
approval in between, which removes the adversarial separation the epic is built
on — and a self-authored plan is self-consistent, so a bad one is harder to
catch, not easier. With it, the backlog still unblocks itself and the separation
survives, at the cost of one extra model call per drafted plan.

**The challenge facet is not a new kind of agent.** It is a one-shot headless
model call inside the existing triage job — no tmux window, no worktree, no
claim, no session row. The triage job already works this way today. The
one-task-one-window-one-session invariant is untouched, and this is explicitly
not the larger adversarial-agents effort, which is about agents as durable
participants.

## Open questions

None.


