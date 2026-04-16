# Endless: Post-Archon Discussion Summary

**Date:** April 15, 2026
**Context:** Follow-up discussion after completing the Archon vs. Endless competitive analysis

---

## 1. Claude Code Tasks System — Impact on Endless

### What We Learned

Claude Code shipped a Tasks system in January 2026 (v2.1.16) that replaced the simpler Todos system. Anthropic explicitly credited Steve Yegge's Beads project as inspiration. The key capabilities of Tasks are: persistent storage at `~/.claude/tasks/`, dependency DAGs between tasks (Task A blocks Task B), status tracking (pending, in_progress, completed, blocked), multi-session coordination via shared `CLAUDE_CODE_TASK_LIST_ID` environment variable, and broadcasting updates across sessions watching the same task list.

### What We Decided

Tasks, Beads, and Endless each operate at a different scope, and the community has already worked this distinction out clearly. Tasks handles **session-scope** coordination — how do three Claude Code sessions collaborate on one feature? Beads handles **project-scope** memory — what did we do on this project last week? Endless handles **portfolio-scope** awareness — across all twelve of my projects, which ones need attention and what was I trying to accomplish in each?

Neither Tasks nor Beads has any concept of cross-project state, which is Endless's core value. However, Tasks' dependency tracking and enforcement within a single project may reduce the need for some of Endless's enforcement hooks at the session level. Endless's "declare your intent before editing" enforcement remains unique — Tasks enforces execution order, Endless enforces motivational clarity.

### Action Item

A prompt was created for Claude Code to analyze the specific overlap between Tasks and Endless's plan system. The prompt asks Claude Code to read both the Tasks documentation and the Endless codebase, then produce an honest assessment of what overlaps, what each does better, and where integration opportunities exist. The prompt file was saved as `tasks-vs-endless-analysis-prompt.md`.

---

## 2. Intent Declaration as a Novel Concept

### What We Discussed

Surprise was expressed that Endless's enforcement of "declare your intent before editing" appears to be novel among harness builders. Upon analysis, the reason became clear: existing enforcement tools all operate post-decision or mid-execution. Archon enforces process ("follow these steps in this order"). Beads enforces execution order ("Task B waits for Task A"). Git hooks enforce code quality ("lint must pass before commit"). PR templates enforce documentation ("describe what changed").

All of these assume the developer has already decided what they're doing. Endless intervenes at the pre-decision moment — asking "what are you trying to accomplish?" before any code changes happen. The closest analog is an aviation departure briefing, where the crew confirms intent and route before the wheels start moving.

### Why It's Not Widely Implemented

Most harness builders solve for teams where intent is communicated through tickets, PRs, and standups. A solo developer has no external accountability system forcing them to articulate intent. Endless fills the gap that teams fill socially.

---

## 3. Positioning and Naming

### The Category Name

The term "situational awareness" was selected as the category descriptor for Endless. It borrows from aviation and military operations where practitioners manage many concurrent streams simultaneously, and it immediately communicates: "I have too many things going on to track in my head, and this system keeps me oriented."

Situational awareness in aviation has three levels that map to Endless's capabilities: Level 1 (perception — knowing which projects exist and what's active), Level 2 (comprehension — understanding that a project is drifting or a document is stale), and Level 3 (projection — anticipating what will happen if a project goes untouched for another week).

### Alternatives Considered and Dismissed

**"Focus multiplier"** — Dismissed because "focus" implies concentration on a single thing, when Endless is about maintaining breadth of awareness across many things. Also, Endless doesn't multiply focus; it prevents the loss of orientation that makes focus impossible.

**"Continuity engine" / "continuity system"** — Considered strong because the thing that breaks across 10+ projects is continuity of intent. However, "continuity" felt too abstract and didn't evoke the right mental image for most people.

**"Coherence"** — Technically accurate (coherence is the active resistance to entropy in unmanaged systems), but dismissed because it doesn't evoke anything concrete. Hearing "coherence" doesn't make someone picture the problem Endless solves.

**"Orientation"** — Considered a strong backup to "situational awareness." When you have 10 projects and you've lost track of three of them, you think "I don't know where I am," and orientation captures that visceral feeling. However, "situational awareness" was preferred as the primary term because it carries more weight and specificity.

### The Tagline

**Final decision:**

> **Endless: Many projects, all at once.**

This tagline won because it makes a promise that sounds slightly impossible, which creates curiosity ("wait, how?"). It's five words, making it memorable and repeatable. It works at every level of explanation — standalone on a README, or as the opening to a longer conversation.

### Alternatives Considered and Dismissed for the Tagline

**"Keep Track of Everything"** — Strong on clarity but too generic; it could describe Notion, Jira, or a filing cabinet. Doesn't convey the project portfolio dimension.

**"Conquers chaos" / "Manage Multiple Project Chaos"** — Has energy but is heavily used in productivity marketing. Every project management tool claims to conquer chaos.

**"Become a 10x Project Coder"** — Fun but "10x" carries baggage in developer culture and triggers eye-rolls.

**"Every project, always in check" / "Every project, in control"** — Sounds like monitoring tools, evoking dashboards with green and red lights. Undersells the plan tree and enforcement dimensions.

**"Rein in your project portfolio"** — Captures the chaos-taming feeling well, but "portfolio" felt corporate.

**"Always know what's next"** — Sounds like a task manager with a linear queue, which isn't what Endless provides.

**"Project portfolio Zen"** — Zen implies simplicity and minimalism, but managing 10+ projects is inherently not that. Sets an expectation Endless can't deliver.

**"Herding Cats. Successfully."** — Memorable and funny, but positions Endless as wrestling with something inherently uncontrollable, undermining confidence. Also doesn't mention projects, so it lacks context when seen in isolation.

**"Do more parallel projects" / "Multiply your Parallel Projects"** — Implies Endless helps start more projects, when it actually helps you not drown in existing ones.

**"Parallel projects no longer overwhelm"** — Framed negatively; people remember what something does, not what it prevents.

**"Many projects, all at once"** — Selected as the winner from the brainstorming session.

### The Description

**Final decision:**

> Endless allows managing a myriad of software projects using AI without losing track of the details.

This description was selected after iterating through several alternatives focused on different aspects of the problem.

### Alternatives Considered and Dismissed for the Description

**"...without losing sight of the big picture"** — Dismissed because the problem isn't losing the big picture. You don't forget that Project X exists. You forget what plan you were working on, what you decided in a Claude conversation last Tuesday, and what the next step was. Those are details, not the big picture.

**"...without getting lost in the minutia"** — Dismissed because "minutia" connotes trivial details, and the lost details aren't trivial. Forgetting which constraints a research doc established can derail an entire project.

**"...without getting caught in rabbit holes"** — Dismissed because rabbit holes are only one failure mode among several. Endless also prevents forgotten projects, lost decisions, stale documents, and orphaned plans.

**"...without losing track of objectives"** — Accurate but generic; any to-do list claims to track objectives.

**"...without losing your place"** — Interesting metaphor (like losing your bookmark in a book), but felt too casual for the description line.

The word "myriad" was specifically chosen over "many" or "multiple" because it conveys abundance with a subtle connotation of overwhelming variety — exactly the feeling Endless addresses.

### Full Positioning Package

> **Endless: Many projects, all at once.**
>
> Endless allows managing a myriad of software projects using AI without losing track of the details.

---

## 4. MCP Server vs. CLI with --json

### What Was Discussed

The Archon analysis recommended exposing Endless as an MCP server. This was challenged on practical grounds.

### What We Decided

CLI with `--json` output on all commands is the right approach for now. The reasoning: a CLI is debuggable with `| jq`, testable with bash scripts, usable by any agent that can run commands, and understandable by a human reading a terminal. MCP adds discoverability and schema validation, but those are nice-to-haves. MCP servers are harder to debug than CLIs, and humans can't use an MCP directly to verify it's working.

The only surviving argument for MCP is ecosystem reach to tools the developer isn't currently using (e.g., Cursor, Codex). This was identified as a post-MVP concern. Beads solved the same problem by writing a thin MCP wrapper that shells out to their CLI under the hood — Endless could do the same later if needed.

**Decision: Build `--json` output on all CLI commands. Defer MCP to post-MVP.**

---

## 5. Archon Integration — The Jeep and Porsche Analogy

### What Was Discussed

The question was raised whether Archon and Endless would be used simultaneously or separately, like owning a Jeep 4x4 and a Porsche 911 — you might own both but use them for completely different activities.

### What We Decided

The analogy is mostly right. The more precise version: Archon is a power tool you pick up for a specific job. Endless is the workbench that's always there — it holds your projects, shows you what's next, and tracks what happened. You don't "use the workbench" the way you use a power tool. The workbench is the surface you work on, and Archon is one of many tools you might reach for while standing at it.

The sequential handoff model (Endless identifies what needs work → Archon executes a workflow → Endless records the result) is plausible but premature. Trying to integrate Endless with Archon or h2pp right now would be premature integration — debugging the interface between two systems that are both still evolving.

**Decision: Do not build Archon-style workflow orchestration into Endless. Do not attempt integration until Endless is independently stable. Follow the Bitter Lesson: pave the cowpaths once natural handoff patterns emerge from real usage.**

---

## 6. Cost Control

### What Was Discussed

The Archon analysis suggested implementing per-session cost tracking as a feature borrowed from Archon's per-node `maxBudgetUsd`.

### What We Decided

As long as Endless operates standalone (without programmatic session spawning via Archon or similar), human cognition is the natural rate limiter. A solo developer can only track so many tmux windows. Cost caps only become relevant if Endless ever integrates with a harness that spawns sessions without human initiation. This is not a current concern.

---

## 7. h2pp and Go Strategy

### What Was Discussed

Time has already been invested in h2pp (a Go-based harness tool built on top of h2) for managing workflows, particularly for developing a website for a Go library. However, using h2pp with Endless feels overwhelming given the current state of Endless development.

### What We Decided

Endless is being prototyped in Python for faster iteration. Once the design and implementation are stable, the plan is to port to Go. The port milestone is tied to significant distribution — distributing and supporting Python is substantially harder than distributing a single Go binary. Having h2pp in Go is a strategic advantage for eventual integration, since Endless already has Go components (endless-hook, endless-serve), meaning future integration could use shared libraries and direct function calls rather than shelling out.

**Decision: Keep using Python for prototyping. Port to Go when distribution becomes a priority. Do not attempt h2pp integration until Endless is working smoothly on its own.**

---

## Summary of All Decisions

1. **Tagline:** "Endless: Many projects, all at once."
2. **Description:** "Endless allows managing a myriad of software projects using AI without losing track of the details."
3. **Category term:** "Situational awareness" is the conceptual category; "orientation" is the plain-language backup.
4. **Claude Code Tasks:** Run the analysis prompt from the Endless repo to map specific overlap. Portfolio-scope awareness remains Endless's unique contribution.
5. **Intent enforcement:** Confirmed as a genuinely novel concept among harness builders. Keep building it.
6. **MCP vs. CLI:** Build `--json` on all CLI commands. Defer MCP to post-MVP.
7. **Archon integration:** Do not build. Do not attempt until Endless is independently stable.
8. **Cost control:** Not a current concern for a solo-developer tool.
9. **Go port:** Triggered by distribution needs, not feature completeness.
