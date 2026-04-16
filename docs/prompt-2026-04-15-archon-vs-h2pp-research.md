# Research Prompt: Archon vs. h2 + h2pp — Comparative Analysis

## What I need

A deep compare-and-contrast analysis between two approaches to orchestrating AI coding agents:

**Archon** — an open-source "harness builder" that defines multi-step workflows as YAML DAGs and orchestrates Claude Code (and soon Codex) sessions through them. Created by Cole Medan. Repo: https://github.com/coleam00/archon — Website: https://archon.ai

**h2 + h2pp** — a two-layer stack where h2 (https://github.com/dcosson/h2) is a Go-based agent runner handling process management, inter-agent messaging, and multiplexing, while h2pp adds a "direction layer" on top: recipes (reusable markdown work briefs with YAML frontmatter), plans (ad-hoc instructions to running pods), session tracking, per-role state, and a recommendation feedback loop. h2pp composes h2's existing commands (`pod launch`, `send`, `list`, `attach`) without modifying h2 internals. It is currently a Python/Click prototype; the target is a Go port offered as PRs to h2 itself.

The analysis should help me understand where these tools genuinely compete, where they're complementary, and what architectural ideas I should consider borrowing from Archon into h2pp — or explicitly reject.

## Context you should know before researching

### What h2 provides (Tier 1–2, working)
- Process management: launching, monitoring, multiplexing AI coding agents as background processes via TTY wrapping
- Roles: YAML files defining model, instructions, permissions for individual agents
- Pods: groups of agents launched from templates (like `dev-pod` with manager + workers)
- Inter-agent messaging with priority levels (interrupt, normal, idle-first, idle)
- beads-lite (`bd`): lightweight Git-native issue tracker (JSON files)
- Tier 3 (orchestration) is described in h2's README as "still very much a work in progress"

### What h2pp adds (the direction layer)

h2pp is currently a **Python/Click CLI prototype** (ported from an earlier ~4,500-line bash proof-of-concept). Python is a prototyping language here, not an end goal — the plan is to port stabilized functionality to Go and offer it as PRs to h2, filling h2's unfinished Tier 3 orchestration layer. If the PR is not accepted, h2pp becomes a standalone Go tool.

**Recipes** are reusable markdown files with YAML frontmatter. A recipe specifies which pod to launch, what variables to pass, the work brief (goal, instructions, constraints), and acceptance criteria. The brief goes to the manager agent; the criteria are parsed separately and routed to the evaluator agent. Recipes live in `~/.h2/recipes/` (global) and `.h2/recipes/` (per-project), with local overriding global. They are designed to be run many times and refined between runs.

**Plans** are ad-hoc, one-time instructions — typically from Claude's `~/.claude/plans/` directory. Picked interactively and sent to a running pod's manager.

**Sprint contracts**: before work begins, the manager and evaluator negotiate an agreed-upon interpretation of the recipe's acceptance criteria. The evaluator may refine vague criteria into testable conditions but cannot weaken non-negotiable ones.

**Six built-in roles** with a hub-and-spoke communication model: Manager (coordination only, never reads source code), Evaluator (independent verification, classifies results as PASS/FAIL/PARTIAL), Code-Auditor (gap analysis, exception reports only), Docs-Writer, Coder (verifies examples compile against real API), Icon-Designer. Workers do not talk to each other directly — the manager delegates, monitors, and routes all communication via h2's messaging system.

**Sessions** track recipe runs. Session capture detects completion status via exact markers (`SESSION_COMPLETE: ALL CRITERIA PASS`) or fuzzy natural-language matching. Background resolution automatically checks for stale "running" sessions and marks them completed or failed. Noise filtering removes low-value agent chatter using configurable patterns.

**Per-role state** files (JSON in `.h2/state/`) track role-specific progress between runs (e.g., last audit timestamp). State persists across sessions.

**Recommendations**: after each run, the evaluator writes recommendations from three sources — sprint contract refinements, evaluation findings, and worker post-mortems. `h2pp recommend resolve` launches Claude Code interactively to walk through each recommendation; users accept, reject, or skip each one. Accepted changes are applied to the recipe; resolved items are archived. Recommendations live in both local (`.h2/recommendations/`) and global (`~/.h2/recommendations/`) scopes.

**Monitoring**: `h2pp watch` provides split-pane monitoring with real-time status and a 1-9 agent switcher. `h2pp view` opens each agent in its own Ghostty tab. `h2pp status` shows a dashboard with agent counts, recipes, recommendations, and token usage.

**Zero footprint**: h2pp stores nothing of its own — everything lives in h2's existing `.h2/` directory structure. This is a deliberate design decision to make the eventual PR into h2 as clean as possible.

**Architectural posture**: h2pp composes h2's existing commands into higher-level workflows with zero scheduling logic and zero workflow engine. It does IO (reading recipe files, calling h2 commands), structural work (parsing YAML frontmatter, selecting pods), and policy (which recipe to run, which pod to target). All semantic decisions belong to the agents. This is textbook ZFC per Yegge's Zero Framework Cognition thesis.

### What Archon provides (from my prior research)
- YAML-defined DAGs where each node is either an AI prompt sent to a Claude Code session, a deterministic bash command, or a human approval gate
- Nodes in the same topological layer run concurrently via `Promise.allSettled`
- Per-node model selection (Opus for architecture, Haiku for classification), per-node MCP server config, per-node cost caps
- Context mode per node: fresh session or continued conversation
- 17 default workflows covering PR lifecycle, issue fixing, code review, PRD creation
- Six platform adapters: CLI, Web UI, Slack, Telegram, Discord, GitHub webhooks
- Web dashboard for monitoring running workflows and viewing logs
- A "workflow builder" workflow for creating new custom workflows
- Archon skill for Claude Code so agents can invoke Archon workflows
- Built on Bun/TypeScript after April 2026 rewrite; ~17.9k GitHub stars
- Cole Medan frames this as "harness engineering" — the third era after prompt engineering and context engineering

### My design philosophy (important for evaluating tradeoffs)
- **Zero Framework Cognition**: h2pp is a thin deterministic shell. All semantic decisions belong to agents, not to the wrapper. Every piece of cognitive logic added to the harness encodes an assumption about what models can't do, and those assumptions depreciate fast.
- **Adaptation over adoption**: I selectively extract ideas from existing tools into bespoke solutions rather than wholesale-adopting others' work.
- **The Bitter Lesson applied**: general methods leveraging computation beat human-engineered structure. Don't over-prescribe the workflow because model capabilities change rapidly.
- **h2pp extends h2 without modifying it**: composing existing commands, not forking or wrapping internals. Currently prototyping in Python; the target is a Go port with most or all functionality offered as PRs to h2's Tier 3 orchestration layer. Python is a prototyping language, not an end goal.

## Specific research questions

### 1. Architectural comparison
How do Archon's YAML DAGs compare to h2pp's recipe+plan model as mechanisms for directing agent work? Archon prescribes the full execution graph in advance; h2pp hands a structured brief to a manager agent and lets the agent decide how to execute. What are the concrete tradeoffs of each approach — for reliability, flexibility, token efficiency, and maintainability as models improve?

### 2. The determinism question
Archon's core value proposition is making AI coding "deterministic and repeatable." h2pp deliberately avoids deterministic workflow logic, trusting the agent to decompose and execute. Research the empirical evidence: does deterministic orchestration (Archon-style DAGs, Stripe Minions, Anthropic's harness research) actually outperform structured-brief-to-smart-agent approaches? Or is the evidence mixed? What does the latest research say about when determinism helps vs. when it over-constrains?

### 3. The "harness engineering" narrative
Medan positions Archon as the defining tool of the "harness engineering" era. How widely adopted is this framing? Is it gaining traction beyond Medan's audience? Do other projects (Anthropic's agent teams, OpenAI's Codex orchestration, other open-source tools) use similar framing or different vocabulary for the same concept? Is h2+h2pp a harness by this definition, or something categorically different?

### 4. Workflow reuse vs. recipe reuse
Archon workflows are versioned YAML committed to repos — the whole team runs the same process. h2pp recipes are personal markdown files that improve through use via the recommendation loop. Compare these as mechanisms for encoding and sharing development process knowledge. What are the tradeoffs for a solo developer who might eventually work on a team?

### 5. Token economics
Archon runs many Claude Code sessions per workflow (each node can be a fresh session). h2pp sends one brief to a manager who orchestrates within a single pod. Research the actual token consumption patterns. Archon claims model tiering (Haiku for classification, Sonnet for implementation) offsets the multi-session overhead. Is this true in practice? What do users report?

### 6. What Archon does that h2pp cannot (and whether it matters)
Identify capabilities Archon provides that h2pp structurally cannot — not "doesn't yet" but "cannot without fundamental redesign." For each, assess whether a solo developer managing many parallel projects actually needs it, or whether it's solving a team/enterprise problem.

### 7. What h2pp does that Archon cannot (and whether it matters)
The reverse: what does the h2+h2pp architecture enable that Archon's DAG model structurally cannot? Consider: h2's inter-agent messaging with priority levels, the hub-and-spoke communication model where a manager coordinates specialist workers in real time, the recommendation feedback loop where recipes improve through use, session tracking with completion detection, per-role persistent state, the manager agent's autonomy to adapt execution based on what it discovers mid-task, and the sprint contract negotiation between manager and evaluator before work begins.

### 8. Integration potential
Could these tools work together? Specifically: could Archon workflows invoke h2 pods as execution engines (instead of raw Claude Code sessions)? Could h2pp recipes trigger Archon workflows for well-defined sub-tasks? What would the integration surface look like?

### 9. Community and trajectory
Compare the projects' communities, contribution patterns, and development velocity. Archon has mass-market appeal (17.9k stars, YouTube content). h2 is niche (14 stars, 2 contributors). What does this mean for long-term viability, ecosystem support, and the likelihood that upstream changes break downstream tools?

### 10. The PR strategy question
I plan to port h2pp to Go and offer most or all of its functionality as PRs to h2's Tier 3 orchestration layer. Does Archon's existence change that calculus? Would h2's author see Archon-style DAG workflows as a more attractive Tier 3 than h2pp's recipe model? Research h2's stated philosophy and assess which approach aligns better with h2's existing architecture and the author's design sensibilities.

## What I do NOT need

- A recommendation to abandon h2pp and adopt Archon wholesale. I've already decided to continue h2pp. I need the analysis to inform what to borrow, what to reject, and how to position h2pp relative to Archon.
- Surface-level feature comparison tables. I need architectural analysis of the underlying design philosophies and their implications.
- Coverage of Endless (my other project). I have a separate analysis of Archon vs. Endless already completed.
- Coverage of Clappie, claude-squad, dmux, or claude-flow. These were covered in prior research.

## Sources to investigate

- Archon repo: https://github.com/coleam00/archon — especially the workflow YAML files in `/workflows/`, the CLI source, the skill definition, and the README's philosophy sections
- Archon docs or wiki if they exist
- h2 repo: https://github.com/dcosson/h2 — especially the README's Tier 3 discussion, the pod/role system, and the beads-lite integration
- Cole Medan's other content on harness engineering (YouTube, blog, Twitter/X) for his latest thinking on where Archon is heading
- Community discussions: GitHub issues/discussions on both repos, Reddit threads, Discord/Slack communities if findable
- The Anthropic harness engineering blog post: https://www.anthropic.com/engineering/harness-design-long-running-apps
- Steve Yegge's ZFC thesis: https://steve-yegge.medium.com/zero-framework-cognition-a-way-to-build-resilient-ai-applications-56b090ed3e69
- Any empirical studies on deterministic workflow orchestration vs. agent-autonomy approaches for code generation quality
- Stripe Minions coverage for evidence on harnessed vs. unharnessed PR acceptance rates

## Output format

Write the analysis as a narrative research report with clear section headers. Lead with a synthesis paragraph that captures the essential tension between the two approaches. Use the specific research questions above as the structural backbone, but synthesize rather than mechanically answering each one. End with a "What to borrow, what to reject, what to watch" section that gives me concrete, actionable guidance for h2pp's development.
