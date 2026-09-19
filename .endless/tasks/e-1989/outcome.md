# Synthesis — redesigning task content, plan requirements, and triage

Produced E-1991 (epic) with four children: E-1992, E-1993, E-1994, E-1995.

## The diagnosis we landed on

The original field model was sound — title recognizes, description explains,
plan instructs. Agents collapsed all three into whichever field they reached
first. Every subsequent change was a containment measure against that behaviour
rather than a correction to the model: the 100-char title cap, the 1000-char
description cap, `untriaged`, the triager, the routing sweep, `--keep-status`,
the description-edit reset. The structure was never wrong; it was unenforced,
and enforcement kept being attempted from the outside with progressively
cleverer machinery.

That framing was tested during the brainstorm and partly survived. Character
limits alone would not have fixed it — an agent compresses a mini-plan rather
than abandoning it. But **the cap plus a legitimate home for the plan removes
the motive**, not just the space: a description is only overloaded today because
it might be all there is. Once a plan is required for a task to go anywhere,
there is nothing to smuggle.

## What changed my mind during the discussion

**On why agents leave open questions in plans.** My initial diagnosis — that an
agent never registers the choice as a choice — was wrong for the observed case.
The actual sequence is: agent files the plan *and* lists its open questions;
told "it is not a plan if it has open questions," it decides them; told "do not
decide, ask." That is a **turn-completion** failure, not a noticing failure. The
agent was instructed to file a plan, and ending a turn with nothing filed reads
as failing the instruction, so it files something and annexes the questions as a
footnote. Step two's correction can only be satisfied in-turn by making the
questions disappear.

The fix follows directly and is now E-1993 §3: make **"plan drafted, N questions
open" a legitimate, complete, filed state**. The agent's turn succeeds, the
questions land durably instead of scrolling away in chat, and filing becomes the
way to ask rather than the opposite of asking. Nothing in the guide or either
CLAUDE.md currently teaches any of this behaviour — it is the model's ungoverned
default, which is why the good half and the bad half arrive from the same agent
on consecutive turns.

**On `spec` as the umbrella term.** I proposed it; web research showed the
concern about it was well-founded. Spec-driven development has a settled 2026
meaning in which spec and plan are *distinct stages* — spec is what and why,
plan is how. Using `spec` as an umbrella over the plan inverts that. Umbrella
term is therefore `plan`.

**Endless has no spec, and a description is not one.** I initially wrote that
Endless's description/plan split "already is" SDD's spec/plan split. That is
wrong and was corrected. An SDD spec is **long and detailed and specifies in
full** what an implementation must achieve. An Endless description is **short and
pithy** — enough for a user to recognize and understand a task's goal, and no
further. A 256-character field can never be a spec. The correct statement is
narrower: Endless does not *conflict* with SDD, which is not the same as being
aligned with it. Endless has title, description, and plan; it has no spec
artifact and does not require one.

Endless will not require SDD. No `spec` slot ships — an unused content slot is
a field agents will find and fill, and SDD's actual premise (code as a
regenerable output of the spec) is a far larger commitment than task tracking.
The closed-per-release enum makes it a one-release addition behind a config flag
if anyone ever asks.

**On session persistence.** I claimed sessions do not survive reboots. They do —
transcripts persist and resume from disk. That correction strengthened the
design rather than merely fixing an error.

**On primed sessions exiting.** I proposed the read-through session exit after
recording its questions, and resume from transcript later. Wrong: **an exited
session cannot receive messages**, and E-1995's dispute resolution runs over
exactly that channel. Live primed sessions are enabling infrastructure, not an
artifact. Cost is accepted for now; if it bites, the graceful fix is
live-by-default with least-recently-needed eviction and resume-on-demand, since
an evicted session loses reachability but not context.

**On one dispatching verb for spawn/resume.** I proposed it; the footgun
objection is correct. Verbs stay explicit and the wrong one refuses with the
right one, git-style — nothing is ever inferred, every path is one copy-paste
away, and the refusal surfaces the state you did not know about.

## The ideas that carry the epic

**Named slots AND character limits — both, deliberately.** The strongest
structural idea in the discussion generalizes past acceptance criteria: give
every commonly expected, well-known part its own content row. Limits fight
overloading with scarcity; **slots fight it with placement**. The two are
belt-and-suspenders and neither replaces the other: without slots an agent
compresses a mini-plan into whatever field is available, and **without limits an
agent errs toward verbosity** — titles that wrap, descriptions that make a basic
`task show` span multiple screens. Both mechanisms ship. An agent cannot dump acceptance
criteria into the plan when acceptance criteria have a row of their own, because
the right answer becomes more obvious than the wrong one. This reframes E-1531
from "flexible typed storage" — the weaker framing — to an enumerated set of
slots that pre-empt overloading, with a closed per-release enum as the
enforcement. Agents cannot invent a type; releases add them.

**The read-through session is the implementing session.** Rather than a
throwaway evaluator, triage spawns the real implementation session and stops it
before it implements. This dissolves the calibration problem rather than solving
it: there is no evaluator-versus-implementer gap when they are the same context.
And the open questions arrive **while the user's context on that task is still
warm** — the actual scarce resource, more than calendar lead time.

**The sufficiency question, in one sentence:**

> Is this plan sufficient, without open questions, to allow this task to be
> performed?

Same question for every task type; the standard for "sufficient" is
type-dependent. For a brainstorm the plan is the framing to be explored, so "no
implementation steps" can never mean "needs a plan first" (E-1946's bug).

**One spawn mechanism, two terminal instructions.** E-1812/E-1813/E-1814 already
own auto-spawn keyed on complexity and risk. Priming is not a second system:
*low complexity and low risk run to completion; everything else pauses for input
from the user.* Same plumbing, same read-through, ratings choose the ending.
Start conservative; widening the run-through band is a threshold change, not new
machinery.

**The user is currently the message bus.** The worst possible allocation — the
person with the least context routing between two agents, where the filing
session holds context neither the user nor the objector has. E-1995 automates
the workaround already being performed by hand. The filer is the right recipient
because it is not deciding unilaterally; it is answering a challenge on the
record. Only genuine deadlocks reach the user, carrying both positions — which
produces the pros-and-cons format that prompted this brainstorm, as a byproduct.

**Two safeguards built in from the start**, not retrofitted: preference-class
questions ("60 characters or 100") short-circuit straight to the user, and a
hard round cap bounds the dispute loop. Two agents arguing with no human in the
path will otherwise consume tokens indefinitely and never surface.

## Decisions recorded

- Title 60 characters, description 256.
- A plan is required **to spawn**, not to file. Requiring one at filing forces
  an agent to plan work it is not doing in an area it may not know, producing
  plan-shaped compliance text — the exact noise the epic removes.
- One stored `plan` type; per-type surface words (`--plan` / `--brief` /
  `--topic`) with `--plan` as a universal alias that always works.
- Acceptance criteria are their own slot, distinct from verify scripts:
  acceptance answers "what does done mean" and is durable; a verify script
  answers "did this run pass" and is a per-task pre-land gate. Acceptance
  criteria are a plausible future *source* for a drafted `verify.toml` (E-1793),
  not the same artifact.
- The re-approval reset retargets from description to plan. A plan change drops
  approval; a description change becomes cosmetic. This also gives E-1995 its
  re-approval hook for free.
- New tasks eager, the existing ~485 grandfathered and migrated lazily by the
  spawn gate. Retroactive evaluation would produce hundreds of simultaneous open
  questions — the time sink the epic exists to remove. Phase cannot discriminate
  either: `now` holds 214 tasks.
- Primed sessions stay alive.

## Left open deliberately

- **Facet names.** `triage` is the job; shape check / read-through / the
  challenge / dispatch are provisional. "Read-through" was named when the facet
  was understood as *simulating* an implementer, which E-1994 collapses —
  nothing is simulated, the session begins and stops. Settling the names is the
  first act of E-1994.
- Open questions live **only** on the tasks that carry them — E-1993 (one),
  E-1994 (two), E-1995 (two) — with pros and cons, none decided. They are
  deliberately not restated here: an open question repeated in two places is one
  question the user has to read and weigh twice.

  E-1994 and E-1995 are therefore held at `unplanned`, not `submitted`. This
  epic's own rule is that a plan with open questions is not a finished spec, and
  filing them as `submitted` contradicted it.

## Findings surfaced along the way, not filed

- **ED-1538 and ED-1539 went `accepted` on 2026-08-14.** E-1813's plan says "do
  not start until they are ratified" and nothing noticed. E-1813 is startable,
  and it gates E-1994.
- **`phase` has stopped discriminating**: 214 `now`, 116 `next`, 118 `later`, 32
  `maybe`, 5 `urgent`. Retiring `next` is a worthwhile simplification but not a
  fix — merging it makes `now` 330. Nothing keeps `now` small. Complexity and
  risk (E-1813) may be the real replacement, being orthogonal to urgency and
  actually consumed by machinery, which is what keeps a field honest. A
  `phase` → `priority` rename was discussed and parked; `horizon` fits the
  values (urgent/now/later/maybe) better than either. Not filed, per request.

## Dispositions

- E-1531 and E-1562 reparented under E-1992 (real content, kept).
- E-1946 replaced by E-1994; E-1948 and E-1970 replaced by E-1995. Their
  substance is folded into those plans, verbatim where it survives.
- E-1844 untouched — three of its four children are `assumed`; it is a finished
  epic, not a container to reuse.
- E-1808's auto-spawn line (E-1812/E-1813/E-1814) left in place. E-1994 consumes
  it rather than swallowing it.
