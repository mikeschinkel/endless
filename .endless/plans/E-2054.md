# Brief: Multi-Perspective Research with Reconciliation

*Provisional and exploratory. This is a problem statement plus decision criteria, not a settled plan. Do not begin implementing. Read it, then come back with an assessment and open questions.*

## Reserved terms

`spawn`, `task`, `session`, and `land` carry specific Endless meanings. Do not reuse them loosely.

In particular, **`spawn` means only `endless task spawn`**: launching a new tmux window and running Claude in a pane of it with a generated handoff document. It is used exactly once in this brief, in constraint 2, and it refers to that command.

The independent investigators described below are **researchers**, and the session **delegates to** them. They are not spawned, they are not Endless sessions, and they must never become either.

## Why you are getting this

Your previous pass solved this entirely inside Endless's existing primitives — tasks, sessions, worktrees, handoffs. That was too narrow. Claude Code ships several native multi-agent surfaces, and the right design almost certainly composes one of them with Endless rather than reimplementing it. Before proposing anything, read these:

- https://code.claude.com/docs/en/agents (comparison of all four surfaces)
- https://code.claude.com/docs/en/sub-agents
- https://code.claude.com/docs/en/workflows

## The goal

When a research task begins, the same research question should be worked by several independent researchers, whose findings are then reconciled — including an adversarial round where each defends its findings against the others' — producing **one** answer.

Stated as an outcome: the user should get a single reconciled result whose confidence is *earned*, where disagreements between independent investigators were surfaced and resolved rather than averaged away. The user should not see three reports and be asked to reconcile them. That would move review burden onto the human, which is the opposite of what Endless exists to do.

**This must work for the general research task.** Some research questions are answered entirely from outside the project (how does library X behave, what is the current state of technique Y). Some are answered entirely from inside it (why was this decided, what does this subsystem actually do, what has already been tried). Many are mixed. A design that assumes one shape and degrades badly on the other is not acceptable.

## Non-negotiable constraints

1. **Endless is a product, not a personal setup.** Any design requiring the user to hand-place files in `.claude/agents/`, `.claude/commands/`, or `.claude/workflows/` is disqualified unless Endless installs and maintains those files itself, and the ongoing management burden of doing so is explicitly justified. Prefer designs where the behavior ships inside the Endless binary.
2. **Delivery is the handoff document, not a slash command.** `endless task spawn` is the delivery vehicle: the instruction to fan out arrives as part of the generated opening prompt when the task is spawned. The user does not type anything extra. This constraint is about how the *instruction* reaches the session — see constraint 3 for what it must not imply about the researchers.
3. **The researchers are not Endless sessions.** They must not be spawned, must not get tmux windows, worktrees, or sandboxes, and must not appear in the session monitor. They produce no commits, need no isolation, and have nothing to verify or land. They are internal machinery of the one task that is already underway.
4. **It must look like one session with one outcome.** Intermediate reports, rebuttals, and scratch work stay out of what the user reads.
5. **Use the handoff template system that already exists.** Do not invent a parallel mechanism for injecting per-role instructions.
6. **Do not make this a hard dependency of core Endless.** Research tasks that do not want multiple perspectives must keep working unchanged.

## Definitions

**Lens** — one independent angle on the research question, defined by the evidence it is allowed to draw on and the stance it takes. One researcher works one lens. Lenses are chosen per task; there is no fixed set (see *Lens selection*).

**Evidence bundle** — the input package Endless assembles for one lens *before* any researcher begins work. It contains the gathered evidence, a manifest of what the gathering criteria were and what was deliberately excluded, and per-entry provenance: where it came from, when it was written, and whether it has been superseded. It exists so that retrieval happens once, deterministically, in code that understands the ledger — rather than N times, in researchers that do not.

Bundles come in two kinds, and the difference is load-bearing:

- **Closed bundle** (ledger-backed and repository-backed lenses). Contents are frozen at assembly time. The bundle is complete: the researcher works from it and needs nothing else. Reproducible, diffable, comparable across runs.
- **Open bundle** (external lenses). Nothing can be frozen, because the web is not a queryable ledger. It degrades to a *starting source set*, a *recency floor*, and the same manifest — the researcher will go beyond it. This is strictly weaker: not reproducible, not complete, and provenance is only as good as what the researcher records as it goes. Do not paper over the difference by calling both "bundles" in the implementation.

## The two candidate mechanisms

### A. Plain subagents, orchestrated by the session

The handoff instructs the session to delegate to N subagents with differentiated briefs, collect their findings, run a rebuttal round, and reconcile. Everything lives in the handoff text. No files outside Endless.

### B. Dynamic workflow

A JavaScript script holds the orchestration; the runtime executes it in the background. Saved to `.claude/workflows/`, invoked from the handoff by name in prose ("Run /triage-research on ..."). The docs describe adversarial cross-checking as a first-class use case, and `/deep-research` is a shipped instance of the pattern.

### Decision criteria

Evaluate against these, and say which wins on each:

| Criterion | Notes |
|---|---|
| **Can the orchestrator call `endless`?** | A workflow script has no shell or filesystem access and cannot load modules; only its agents can run commands. If reconciliation needs to read or write Endless state directly, this likely decides it. |
| **Distribution burden** | B requires a file in the repo or home directory that Endless must place and keep current. A requires nothing. Weigh against constraint 1. |
| **Availability floor** | B requires Claude Code v2.1.154+, a paid plan, and on Pro an explicit `/config` toggle; it can be disabled org-wide via managed settings. A works wherever subagents work. For a product, what is the failure mode when B is unavailable? |
| **Parent context cost** | Under A, every report and rebuttal lands in the session's context. Under B, intermediate results live in script variables and only the final answer reaches the session. Estimate how badly A degrades at N=3 with substantial findings. |
| **Determinism and replayability** | B codifies the orchestration as a script that is read, diffed, and rerun. A re-derives the plan every time from prompt text. |
| **Single-session illusion** | B surfaces a progress view and a task-panel line; A is invisible. Which better satisfies constraint 4? |
| **Blast radius of upstream change** | B depends on a runtime Anthropic controls and is actively changing. A depends on subagents, which are far more stable. |

A defensible outcome is **A now, B as a documented upgrade path**, but argue it rather than assuming it.

## Lens selection

The lens set is chosen **per task**, not fixed. A question about an external library gets no value from a researcher assigned to project history; a question about a past decision gets none from a researcher assigned to vendor documentation. Assigning a lens with nothing in it produces a confident researcher with an empty bundle, which is worse than having one fewer researcher.

Sketch of the space, not a menu to hardcode:

- **Internal-only questions** — lenses partition the project: implementation as it stands, project history and prior decisions, tests and observed behavior.
- **External-only questions** — see the next section; partitioning by topic does not work.
- **Mixed questions** — the natural split is internal versus external, plus a third lens that starts from the gap between them.

**How the lens set gets chosen is an open design question and is not yours to settle.** Candidates: derived from task type metadata, inferred from the question at delivery time, declared by the user at `endless task add`, or some combination. Lay out the tradeoffs and bring them back.

## Two problems that must be solved regardless of mechanism

### 1. Stale and mis-retrieved evidence (the harder problem)

**Multi-agent reconciliation makes this worse, not better.** Researchers that draw on the same bad evidence will agree, and the scheme reads agreement as confidence. Independent-and-wrong is the failure mode this design manufactures. Both halves of the problem must be addressed, and they are not the same problem.

**Internal staleness.** Researchers reading docs, issues, and commit history through Endless commands are currently finding the wrong records, or finding superseded records and treating them as authoritative. The ledger is append-only, so supersession is derivable rather than guessed. Every record handed to a researcher must carry provenance and status: when written, whether superseded, and by what. This is what closed bundles are for.

**External staleness.** A different failure with the same symptom: sources that were accurate when written and are not now, plus a model whose own priors predate the current state of whatever is being researched. Supersession is not derivable here — there is no ledger. Mitigations are weaker and must be stated as such: recency floors in the source set, requiring the researcher to record the publication date of anything it relies on, and treating undated sources as suspect. A researcher asserting current state from training-era knowledge, with no retrieved source behind it, is the specific thing to catch.

Loud failure applies to both. If a lens cannot be assembled, or its evidence is older than the floor, say so rather than proceeding quietly.

Do not treat any of this as a follow-up. If it is not solved, the design produces confident wrong answers.

### 2. Differentiating the researchers ("seeding")

There is no temperature or RNG seed to set. Differentiation must come from inputs.

**When the question spans domains**, separate the evidence bases. Researchers that examined different things cannot converge by coincidence, and their disagreements are informative rather than stylistic. Enforce this **by supply, not by denial**: give each researcher only its own bundle. A researcher with no external sources in hand does not need to be forbidden from searching — which matters, because tool-level denial needs `.claude/agents/` definitions and runs into constraint 1.

**When the question sits in one domain, lens separation by domain collapses**, and this is the case the design most easily gets wrong. Three researchers loose on the same web, or on the same repository, produce correlated findings — and correlation read as consensus is exactly the failure above. Two fallbacks:

- **Partition within the domain by source class rather than by topic.** For external work: primary and vendor documentation; changelogs, issue trackers, and release notes; independent practitioner writeups and third-party analysis. Same question, structurally different evidence, and the disagreements between these classes are usually the informative ones — vendor docs describe intent, issue trackers describe what actually happens. For internal work: the implementation, the recorded history, the tests.
- **Promote role framing from garnish to load-bearing.** Advocate, skeptic, and null-hypothesis stances matter far more when the evidence base cannot be split. Weakest of the levers, and in the single-domain case it may be most of what you have.

Different models per role, where the mechanism supports it, is a further axis worth testing.

Lens definitions and role framings belong in the handoff template system.

## Open questions to resolve empirically before designing

- **Does the `ultracode` keyword fire from Endless's delivery path?** The keyword is an opt-in only for prompts the human typed, and explicitly does not trigger from `-p` or from non-human-stamped SDK input. Endless delivers the handoff as `claude "$(cat file)"` — positional argument. The docs do not say which side of that line this falls on. Test it. If positional-arg delivery counts as non-human, mechanism B is reachable only by invoking a previously saved workflow by name, never by keyword.
- **What does a subagent inherit?** Specifically whether Endless's hooks fire for subagent tool calls, and whether a subagent's tool calls are distinguishable from the session's own at the hook. This determines whether gate-level enforcement is available at all under mechanism A.
- **What is the actual context cost** of three substantial reports plus a rebuttal round in one session, measured rather than estimated.
- **Do source-class lenses actually decorrelate?** Run one external-only question with three researchers split by source class and one with three researchers given identical briefs. If the findings are equally correlated, the single-domain fallback does not work and needs rethinking.

## What not to do

- Do not build a bespoke orchestration layer inside Endless that duplicates subagents or workflows.
- Do not require the user to install or maintain anything under `.claude/`.
- Do not make the researchers Endless sessions. See constraint 3.
- Do not use agent teams. Peer messaging is the only advantage and it is experimental, disabled by default, and provides no worktree isolation.
- Do not use background sessions with cross-session messaging for the researchers. They produce no commits, need no worktree, and have nothing to verify or land; the full session environment earns nothing here.
- Do not hardcode a lens set, and do not assign a lens whose bundle would be empty.
- Do not surface the fan-out to the user as a thing they manage.

## Deliverable for this pass

An assessment, not an implementation: which mechanism wins on each criterion, how lens selection should work, what the closed and open bundle designs should look like, results of the four empirical tests above, and anything in this brief you think is wrong.