# Analysis: Claude Code Tasks vs. Endless Plan System

## Background

This analysis compares Claude Code's built-in Tasks system (TaskCreate, TaskUpdate, TaskGet, TaskList) against Endless's plan/session/monitoring architecture. The goal is to identify genuine overlap, complementary strengths, and integration opportunities.

### What Claude Code Tasks Actually Is

Tasks is a **session-scoped task tracker** designed for Claude Code to organize its own work within a conversation. Key characteristics:

- **Flat list with dependency edges**: Tasks have `blocks`/`blockedBy` relationships forming a DAG, but no hierarchical parent/child structure.
- **Status workflow**: `pending` → `in_progress` → `completed` (plus `deleted`).
- **Owner field**: Tasks can be assigned to named agents (subagents in multi-agent workflows).
- **Shared task lists**: Via `CLAUDE_CODE_TASK_LIST_ID` env var, multiple sessions can read/write the same task list.
- **Filesystem persistence**: Stored at `~/.claude/tasks/`.
- **Metadata**: Arbitrary key-value pairs attachable to tasks.
- **No UI beyond CLI**: Tasks are visible only within Claude Code conversations or by reading the JSON files.

Tasks is fundamentally an **agent coordination tool** — it helps Claude decompose work and track progress within a coding session. It is not a project management system, not a portfolio tracker, and not an enforcement mechanism.

---

## A. Direct Overlap

| Endless Capability | Tasks Equivalent | Assessment |
|---|---|---|
| **Plan trees** (hierarchical parent/child with title, description, prompt) | Flat task list with blocks/blockedBy DAG | **No equivalent.** Tasks has dependency edges but no hierarchy. No `prompt` field, no tree structure, no concept of zooming into sub-plans. |
| **Plan enforcement** (blocking Write/Edit without active plan) | Nothing | **Nothing comparable.** Tasks has no hook system, no enforcement mechanism, no concept of requiring intent declaration before code changes. |
| **Plan spawning** (`plan spawn` → tmux window + Claude session) | Nothing | **Nothing comparable.** Tasks doesn't interact with tmux, terminal sessions, or session lifecycle. |
| **Session tracking** (which Claude session works on which plan, in which tmux pane) | Owner field on tasks | **Minimal overlap.** Tasks can assign an owner string, but doesn't track tmux context, session IDs, expiration, or session state machines. |
| **Activity monitoring** (ZSH hooks, file change detection, throttled recording) | Nothing | **Nothing comparable.** Tasks is unaware of shell activity, file changes, or anything outside its own tool calls. |
| **Cross-project awareness** (single DB spanning all registered projects) | Per-task-list scope; no project concept | **Nothing comparable.** Tasks has no concept of "projects" as entities. A shared task list spans sessions but not projects. |
| **Project registration and status** (path-based registration, dormancy detection, status lifecycle) | Nothing | **Nothing comparable.** |
| **Document management** (scanning, staleness detection, region tracking) | Nothing | **Nothing comparable.** |
| **Notes system** (project/plan-attached notes with resolution tracking) | Task description + metadata | **Trivial overlap.** You could abuse task metadata as notes, but there's no note-specific workflow (resolve, filter by type, attach to projects). |

**Summary**: The overlap is minimal. Tasks is a within-session work tracker. Endless is a cross-project awareness and intent-enforcement system. They operate at different levels of abstraction.

---

## B. What Tasks Does Better

### 1. Dependency DAGs
Tasks has first-class `blocks`/`blockedBy` with arbitrary edges between any tasks. Endless has the `task_dependencies` table in the schema but no UI, no CLI support, and no visualization. **Tasks is ahead here**, though Endless has the schema foundation to catch up.

### 2. Agent Coordination
Tasks is designed for multi-agent workflows — an orchestrator agent can create tasks, assign them to worker agents via the `owner` field, and workers can claim and complete tasks. Endless has no concept of coordinating multiple Claude Code instances working on different subtasks of the same plan.

### 3. Structured Status with Blocking
Tasks enforces a status workflow (`pending` → `in_progress` → `completed`) and respects dependency blocking (a task with unresolved `blockedBy` shouldn't be started). Endless has status fields on plans but the workflow is looser — status transitions are manual and there's no automatic blocking based on dependencies.

### 4. Session Sharing
Via `CLAUDE_CODE_TASK_LIST_ID`, multiple Claude Code sessions can see the same task list and coordinate. Endless tracks sessions but doesn't share plan state between concurrent Claude sessions working on the same project.

### 5. Zero Setup
Tasks works out of the box in any Claude Code session. No registration, no hooks, no database. For quick within-session task tracking, it's frictionless.

---

## C. What Endless Does That Tasks Cannot

These are capabilities where Tasks has no mechanism and no plausible extension path:

### 1. Cross-Project Portfolio View
Endless maintains a single SQLite database spanning all registered projects. You can see what's active, what's dormant, what depends on what across your entire portfolio. Tasks is scoped to individual task lists with no awareness of "projects" as a concept.

### 2. Intent Enforcement
The PreToolUse hook that blocks Write/Edit unless you've declared what you're working on (`endless plan start <id>`) is architecturally impossible in Tasks. Tasks is a passive tracker — it records what Claude says it's doing. Endless is an active gate — it prevents Claude from doing anything until the human declares intent.

### 3. Human-Driven Plan Evolution
Endless plans are authored by humans (or imported from human-authored markdown), organized as trees that grow organically as the human zooms into complexity. Tasks are created by Claude during a session to decompose work. These serve fundamentally different purposes: Endless captures **what the human wants**, Tasks captures **how Claude plans to do it**.

### 4. tmux Session/Window Tracking
Endless tracks which tmux window/pane is working on which plan, enables spawning new windows for specific plans, and uses tmux options for state persistence. Tasks has no terminal awareness.

### 5. ZSH Activity Monitoring
The prompt hook that records project activity on every shell prompt, with file change detection and throttling, provides a continuous activity signal. Tasks only knows about activity within Claude Code tool calls.

### 6. Document Staleness Detection
Region-level content hashing, dependency tracking between documents, and staleness notes when dependencies change. Tasks has no document awareness.

### 7. Persistent Cross-Session History
Endless's activity table provides a historical record of all work across all sessions. Tasks are ephemeral within their task list — once completed, they're just done flags, not a queryable activity log.

---

## D. Integration Opportunities

These two systems are complementary, not competitive. Here are concrete integration points:

### 1. Plan Start Sets Task List (High Value, Low Effort)

When `endless plan start <id>` activates a session, also set `CLAUDE_CODE_TASK_LIST_ID` to a deterministic ID derived from the plan item. This scopes Claude's Tasks to the active Endless plan. Benefits:
- Tasks created during work are automatically associated with the plan
- Switching plans switches task context
- Multiple Claude sessions on the same plan share a task list

**Implementation**: In the SessionStart hook or the `plan start` command, export `CLAUDE_CODE_TASK_LIST_ID=endless-plan-{plan_id}`.

### 2. Read Task Progress into Plan Status (Medium Value, Medium Effort)

Endless could read `~/.claude/tasks/` to incorporate task-level progress into its plan display. A plan item that's "in_progress" in Endless could show a progress bar based on how many of Claude's sub-tasks are completed.

**Implementation**: In the web dashboard queries, read the task list JSON file corresponding to the active plan and compute completion percentage.

### 3. Enforcement Hook Checks Task Status (Low Value, Low Effort)

The PreToolUse hook could optionally check whether Claude has an active task (not just an active Endless session). This would add a softer enforcement layer: "You have a session but haven't picked a task yet."

**Recommendation**: Skip this. It adds friction without matching Endless's philosophy (human intent, not agent task selection).

### 4. Surface Task Decomposition in Dashboard (Medium Value, Medium Effort)

When viewing a plan item in the web dashboard, show Claude's task decomposition underneath it. This gives the human visibility into how Claude is breaking down the work without requiring the human to manage those tasks.

**Implementation**: Read task list files, match to plan items via the `CLAUDE_CODE_TASK_LIST_ID` convention from #1, render as a collapsible sub-tree under the plan item.

### 5. Use Tasks for Multi-Agent Plan Execution (Future, High Value)

If Endless ever supports `plan spawn` launching multiple Claude sessions for sibling plan items, Tasks could coordinate between them. The parent plan's task list could track which items each session is handling.

---

## E. Recommendations

### Keep Building (Tasks doesn't cover these)

1. **Plan trees and hierarchy** — Tasks is flat. Endless's tree structure with organic growth is a core differentiator.
2. **Plan enforcement** — The intent gate is architecturally unique. Nothing in Tasks can replicate it.
3. **Cross-project portfolio** — The single-database, multi-project view is Endless's raison d'être. Tasks can't touch this.
4. **Activity monitoring** — ZSH hooks, file change detection, tmux tracking. All unique.
5. **Document management** — Staleness detection, region tracking. Fully orthogonal to Tasks.
6. **Web dashboard** — Visual portfolio management. Tasks has no UI.
7. **Session lifecycle** — Start/touch/expire/end state machine with tmux context.

### Integrate Rather Than Duplicate

1. **Dependency tracking**: Don't build a full dependency DAG UI for plan items from scratch. Instead, bridge to Tasks: when a plan item is active, let Claude's Tasks handle sub-task dependencies within that scope. Endless should track **plan-level** dependencies (project A blocks project B), and Tasks should track **task-level** dependencies (implement X before Y within a plan item). Build the `task_dependencies` UI for cross-plan/cross-project edges only.

2. **Status workflow within a plan item**: Don't try to track Claude's moment-to-moment progress on a plan item. Let Tasks handle `pending → in_progress → completed` for sub-tasks. Endless should track the plan item's status at the human-intent level (needs_plan, ready, in_progress, completed, blocked).

### Now Unnecessary

Nothing currently in PLAN.md or DONE.md is made unnecessary by Tasks. The planned features (stable ID matching, plan acceptance detection, web editing) are all orthogonal.

However, **don't build**:
- A sub-task decomposition system within Endless plans — let Tasks handle this
- A within-session progress tracker — Tasks already does this
- Agent coordination features — Tasks + subagents handle this natively

### New Integration Features to Build

**Priority 1** (next sprint): `CLAUDE_CODE_TASK_LIST_ID` integration in `plan start` — low effort, high leverage. Makes every Tasks feature automatically scoped to the active Endless plan.

**Priority 2** (near-term): Dashboard task progress display — read task list files and show completion percentage on active plan items.

**Priority 3** (future): Multi-session coordination via Tasks when `plan spawn` supports parallel execution.

---

## Summary

Endless and Claude Code Tasks are **complementary at different abstraction layers**:

| Layer | System | Purpose |
|---|---|---|
| Portfolio | Endless | Which projects exist, what's active, cross-project deps |
| Intent | Endless | What does the human want to accomplish? (plan trees) |
| Enforcement | Endless | Is the human's intent declared before work begins? |
| Monitoring | Endless | What's happening across all projects and sessions? |
| Decomposition | Tasks | How is Claude breaking down the current work? |
| Coordination | Tasks | How are multiple agents splitting sub-tasks? |
| Progress | Tasks | Which sub-tasks are done within the current session? |

The right mental model: **Endless is the project manager; Tasks is the team lead's whiteboard.** They should talk to each other, but neither replaces the other.
