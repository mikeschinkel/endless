# Fan-out / fan-in for research tasks

## What this is

Fan-out/fan-in is a **shape**, not a feature: split one question into several
independent passes, then reconcile them into one answer. This task is about
whether and how Endless should support a session doing that *inside* a single
task, so a research task's `outcome` benefits from more thinking than one
linear pass produces.

It is NOT about parallelising Endless's own work across tasks.

## Constraints already settled (do not re-litigate)

From Mike, 2026-08-24:

1. **One task, one outcome.** Explicitly rejected: filing multiple Endless
   tasks, or opening multiple tmux windows, to serve one research question. To
   Endless this must still look like an ordinary research task.
2. **Sub-findings are ephemeral.** The intermediate passes do not need a durable
   home in the ledger. Only the synthesised `outcome` is durable.
3. **Harness-specific is acceptable when it adds value.** "Only works in Claude
   Code" is not a reason to reject a design. The handoff template can branch on
   harness and give different instructions where the capability is absent.
   `endless-go`'s `internal/agentenv` (E-1962) already detects the harness and
   answers `Supported()`; the handoff templates already take conditionals
   (`internal/templatecmd/templates/handoff/research.md.tmpl` has `{{if .bg}}`).
4. **`task spawn --bg` is deprecated** — no design may lean on it. (The
   implementing session should check whether the `{{if .bg}}` branch still in
   `research.md.tmpl` is stale and remove it if so.)
5. Parallel agent runs are **not** deterministic, and there is no reason to
   assume passes given different prompts share blind spots. Do not use
   "they'd all miss the same things" as an argument against fan-out.

## Why the obvious answer is necessary but not sufficient

Mike's instinct — teach the shape in the research handoff template — is right
and is the baseline. Its limit: **prose leaves the structure to the agent, so
the structure differs every run.** Nothing establishes that the passes were
actually independent, that their search strategies differed, or that the
synthesis reconciled them rather than averaging them into the blandest common
answer. The failure this is meant to prevent — a confident deliverable that
missed prior art, or ruled against an earlier session without noticing — is
exactly the failure that a fan-out can reproduce if the passes were not
genuinely disjoint.

But over-specifying is its own failure. The value of more thinking is that the
agent picks lenses suited to *this* question. A fixed lens list would defeat it.

**So the design question is: which part of the shape does Endless fix, and which
does it leave to the agent?**

## Options

### 1. Instruct
The research handoff template tells the session to fan out before synthesising.
Endless owns nothing but the prompt; the agent owns lenses, count, and
structure. Cheapest, works in any harness that can spawn sub-agents, degrades to
a no-op elsewhere. Risk: unfalsifiable — you cannot tell from the outcome
whether it happened.

### 2. Contract
Endless states the **contract the fan-out must satisfy**, not the mechanism:
each pass declares the search strategy it used; strategies must differ; the
synthesis must name what it discarded from each pass and why. Still prose, but
*falsifiable* prose — the outcome can be checked against it, by a human or by a
later gate. Endless fixes the contract, the agent picks the lenses.

### 3. Render
Endless emits an **executable orchestration artifact** from a template, the way
it already emits the handoff. Concretely for Claude Code: a `Workflow` script
(the JS file Claude Code's Workflow tool runs), rendered from a template and
parameterised with the task's request text, the pass count, and the schema each
pass must return. "Endless owns the shape" means the orchestration structure —
how many passes, that there is a synthesis stage, what a pass must return — is
Endless's, and the harness only executes it. Strongest guarantee, most coupling
to one harness, and the artifact needs versioning like any other template.

### 4. Record — REJECTED
Endless orchestrates nothing and merely gives the intermediate sub-findings a
durable home on the task, so the debate's inputs survive review. Rejected by
constraint 2: Mike wants the passes ephemeral. Recorded here so it is not
re-proposed.

**Constraint on Render, learned the hard way:** Claude Code's `Workflow` tool
cannot be invoked unless the *user* has explicitly opted into multi-agent
orchestration for that turn (a keyword, or asking for it in their own words). So
Endless cannot cause a Workflow script to run — a rendered script would sit there
until the user asked for it. The `Agent` tool (ordinary sub-agents) carries no
such gate. If Render is chosen, it should render sub-agent orchestration the
session can execute on its own, not a Workflow script.

**Not yet in this list:** Mike put the same question to Claude Web and got
options not covered above. **Those must be folded in before this plan is
approved** — this section is knowingly incomplete.

## To decide before implementing

1. Which option, or which blend? (Instruct and Contract compose; Render
   subsumes both for the harnesses that can run it.)
2. Does the fan-out fire always for `research`, or on a signal — task tier,
   an explicit flag, complexity rating (cf. E-1812 / ED-1538)?
3. What does a session do when the harness cannot fan out? Fall back to a
   single deeper pass, or say so in the outcome so the reader knows the
   deliverable is thinner?
4. Does this generalise to `brainstorm`, `epic`, and audit-shaped `todo`s, or
   stay research-only in this increment?
5. Is there anything the outcome must *state* about how it was produced, so a
   reader can tell a fanned-out deliverable from a linear one?

## Reuse anchors

- `internal/templatecmd/templates/handoff/research.md.tmpl` — the per-type
  handoff template; already conditional.
- `internal/templatecmd/templates/handoff/_mechanics.tmpl`, `_close.tmpl` —
  shared partials, the natural home for a shape shared across task types.
- `internal/agentenv` — harness identity and `Supported()`, from E-1962.
- `endless guide tasks` → "Research-task field model" — `text` is the request,
  `outcome` is the deliverable. A fan-out changes how `outcome` is produced,
  not the field model.

## Out of scope

Cross-task parallelism, background agents, spawn policy (E-1812), and anything
that makes one research question occupy more than one Endless task.
