# Beads and Endless solve different layers of the same meta-problem

**Beads is not a competitor to Endless — it operates one level below.** Steve Yegge's Beads is a distributed, git-backed issue tracker that gives AI coding agents persistent structured memory *within a single project*. Endless is a cross-project awareness system that keeps *the human developer* oriented across 10+ projects simultaneously. They share the same enemy — context loss in AI-assisted development — but attack it from opposite directions: Beads gives agents memory so they can execute; Endless gives the human awareness so they can orchestrate. The most promising path is integration, not competition. Endless could read Beads databases across projects to create a unified awareness layer that neither tool provides alone.

---

## What Beads is and how it works

Beads (CLI: `bd`) was created by Steve Yegge in October 2025, reaching **19,500+ GitHub stars** and tens of thousands of users by April 2026. Written in Go, it's a single binary that initializes a `.beads/` directory inside any project and provides structured task tracking optimized for AI agent consumption.

The core insight is what Yegge calls the **"50 First Dates" problem**: every new AI coding session starts with total amnesia. Beads solves this by storing tasks, dependencies, and notes in a version-controlled database (Dolt) that travels with the git repo. When an agent starts a session, it runs `bd prime` to load ~1-2k tokens of workflow context and `bd ready --json` to get a pre-filtered list of unblocked, prioritized tasks. The agent works, files discovered bugs along the way, and at session end follows a "Land the Plane" protocol — running tests, closing completed tasks, filing remaining work, and syncing to git.

The data model centers on **issues** (called "beads") with hash-based IDs, five dependency types (`blocks`, `parent-child`, `related`, `discovered-from`, `conditional-blocks`), priority levels (P0–P4), and multiple issue types (task, bug, feature, epic, decision, message). The dependency graph enables **topological sorting** — `bd ready` computes which tasks have no open blockers, so agents never waste tokens analyzing what's actionable. A **compaction** system implements "semantic memory decay," summarizing old closed tasks to save context window space.

Beads is explicitly **agent-first, not human-first**. Every command supports `--json` output. There's no built-in UI — Yegge delegates that to the community, which has produced VS Code extensions, terminal UIs, web dashboards, Neovim/Emacs/JetBrains plugins, and orchestration frameworks. The design philosophy is "execution tool, not planning tool" — you plan elsewhere and import into Beads for structured execution.

---

## Shared problem space reveals complementary architectures

Both tools emerge from the same frustration: **AI coding sessions create and then destroy context**. The specific overlapping problems are significant.

**Markdown plan chaos** is enemy number one for both projects. Yegge accumulated 605 contradictory markdown plan files before building Beads. Endless identifies the same pattern as "document entropy" — 5-20+ markdown documents per project becoming stale and contradictory as plans evolve. Both tools replace unstructured markdown with queryable structured storage.

**The rabbit hole problem** appears in both designs. Endless describes it as "zooming into a sub-problem where neither human nor AI remembers the broader context." Beads addresses the same issue through its dependency graph — parent epics maintain context while child tasks are executed, and `bd ready` always surfaces the next logical step regardless of how deep the agent went. Endless solves it with hierarchical plan trees and the `spawn` command, which delegates sub-plans to new tmux windows while the parent session stays intact.

**Session boundary context loss** is the foundational problem for both. Beads persists task state in Dolt/git; Endless persists plan state, activity, and session metadata in SQLite. Both use CLI-first interfaces. Both started with or use SQLite. Both provide mechanisms for AI agents to recover context at session start.

The architectural parallel runs deeper: both use a **tree/graph structure for plans** (Beads: epic → task → subtask via dependency DAG; Endless: plan tree with `parent_id`), both track what work is active, and both aim to make the developer intentional about what they're working on.

---

## Fundamental divergences in philosophy, scope, and target user

Despite the surface similarities, Beads and Endless make **radically different design choices** that reveal distinct visions of AI-assisted development.

**Agent-first vs. human-first.** This is the deepest divergence. Beads is designed for AI agents to consume — JSON output, token-efficient queries, deterministic dependency resolution. The human is a supervisor who occasionally prompts the agent to "check bd ready." Endless is designed for the human developer — a web dashboard shows project status, activity feeds, and plan trees. The human is the primary user who needs awareness; the AI is the tool being supervised. Beads asks "what should the agent do next?" Endless asks "what should the human pay attention to?"

**Single-project vs. multi-project.** Beads initializes per-project (`.beads/` directory) with **no cross-project awareness**. Issues in one project cannot reference issues in another. This is an intentional design choice — git-native means issues travel with code. Endless is fundamentally multi-project: a single SQLite database at `~/.config/endless/endless.db` tracks all projects simultaneously. The entire point is maintaining awareness across 10+ parallel projects. This is perhaps the starkest architectural difference.

**Voluntary vs. enforced.** Beads relies on AGENTS.md instructions and human prompting to get agents to use it. Users consistently report that **agents don't proactively use Beads** — you must say "check bd ready" at session start and "land the plane" at session end. Context rot is a documented problem in long sessions. Endless takes the opposite approach: Claude Code hooks **block Write/Edit operations** unless the developer has registered which plan they're working on. Enforcement is a core design principle, not an afterthought.

**Execution tracker vs. awareness system.** Yegge explicitly says Beads is "an execution tool, not a planning tool." Plans are created elsewhere and imported. Beads tracks what needs doing and what's done. Endless is explicitly "NOT a task manager" — it tracks project-level state, document health, session context, and activity patterns. Plans in Endless are organic, growing structures that the human subdivides; in Beads, they're imported task decompositions with formal dependency chains.

**Distributed vs. centralized.** Beads is git-native and distributed — issues travel with branches, merge with code, sync via git remotes. This enables multi-developer and multi-agent workflows but introduces merge conflicts (a documented pain point). Endless is centralized — one SQLite file is the source of truth for everything. This simplifies the solo developer's workflow but doesn't support collaboration.

---

## Concrete lessons Endless should adopt from Beads

Beads' rapid adoption and community feedback reveal several patterns worth incorporating into Endless.

**The `bd ready` concept is Beads' killer feature.** Computing "what is actionable right now" and surfacing only unblocked work saves enormous cognitive load — for both humans and agents. Endless could implement an equivalent: `endless plan ready` that walks the plan tree, identifies plans with no incomplete child plans, and presents them sorted by priority or staleness. This would directly address the "forgotten projects" problem by surfacing dormant projects that have actionable next steps.

**Dynamic context injection via `bd prime` is elegant.** Rather than maintaining a huge static AGENTS.md, Beads generates ~1-2k tokens of dynamic context at session start — current tasks, project conventions, recent changes. Endless already has plan prompts and the `spawn` command, but could benefit from a similar `endless context` command that generates a dynamic briefing: "You are working on Project X. Active plan: Y. Last activity: 3 days ago. Related documents: Z. Known stale docs: W." This context packet would be invaluable for Claude Code session starts.

**The "discovered-from" dependency type** solves a real problem. When an agent finds a bug while working on a feature, it needs a way to record that discovery without losing focus. Endless could add a `discovered_during` field to plans, creating an audit trail of how work begets more work. This would make the plan tree more informative about *why* certain plans exist.

**Hash-based IDs prevent collision in multi-agent workflows.** Endless currently uses auto-incrementing SQLite IDs. If Endless ever supports multiple concurrent Claude Code sessions creating plans (which `spawn` already enables), hash-based IDs would prevent conflicts. Even for a solo developer, content-addressable IDs would enable **stable reimport** — a feature Endless has explicitly identified as needed.

**The compaction concept maps to Endless's document staleness.** Beads summarizes old closed tasks to save tokens. Endless could apply the same principle to its plan tree: auto-archiving completed plans while preserving a summary, keeping the active plan tree clean and focused.

---

## What Beads doesn't do that validates Endless's existence

Beads has significant blind spots that map precisely to Endless's core value proposition.

**No cross-project awareness.** With 19,500 stars and an extensive ecosystem, nobody has solved this within Beads. Each project is an island. A developer with 10 projects has 10 separate `.beads/` databases with no unified view. Endless's single-database, multi-project design is genuinely novel. No tool in the Beads ecosystem — including the community-built dashboards, TUIs, and VS Code extensions — provides a cross-project awareness layer. This is Endless's strongest differentiator.

**No enforcement mechanism.** Beads community users consistently report that agents **forget to use Beads** in long sessions. The tool "provides memory but you trigger its use." Endless's hook-based enforcement — blocking code changes without plan registration — is a fundamentally different and stronger guarantee. This is not a feature Beads can easily add because it conflicts with Beads' "passive infrastructure" philosophy.

**No session tracking or terminal awareness.** Beads has no concept of tmux sessions, windows, or panes. It doesn't know which terminal is working on which project. Endless's ZSH prompt hooks and tmux integration provide spatial awareness — knowing that "terminal 3 is in project X, last active 2 hours ago" — that Beads cannot provide.

**No document health monitoring.** Beads replaces markdown plans with structured tasks but doesn't monitor the health of project documents (READMEs, design docs, ADRs). Endless's planned document scanning, staleness detection, and research integrity features address a problem that Beads explicitly ignores. Yegge says "plan outside Beads" — but Endless recognizes that the plans, research docs, and design docs *outside* the task tracker are exactly where entropy accumulates.

**No activity monitoring across projects.** Beads tracks task status changes within a project. Endless monitors actual developer activity — file changes, command execution, Claude Code events — across all projects. This activity data enables "which project haven't I touched in a week?" queries that are impossible with Beads.

**The spawn workflow is unique.** `endless plan spawn` creating a new tmux window with Claude Code pre-loaded with a plan's prompt has no equivalent in Beads. Beads agents pick tasks from a queue; Endless orchestrates parallel work sessions with explicit parent-child relationships between the sessions themselves, not just the tasks.

---

## Integration is the right strategy, not competition

The risk assessment yields a clear answer: **Endless and Beads are not competitors, and the risk of building something Beads already solves is low.** They occupy different layers of the development workflow stack.

Beads sits at the **project execution layer** — inside a single project, tracking what tasks exist, what's blocked, what's done. Endless sits at the **developer awareness layer** — above all projects, tracking which projects exist, what state they're in, what documents are healthy, which sessions are active. A developer can and should use both simultaneously. Beads manages the task graph inside Project A; Endless knows that Project A exists alongside Projects B through K, that Project A's research doc is stale, and that the developer hasn't touched Project F in two weeks.

The most valuable integration path: **Endless reads `.beads/` databases across registered projects** to enrich its awareness layer. The web dashboard could show not just "Project X — last activity 3 days ago" but "Project X — 4 tasks ready, 2 blocked, last completed task: refactor auth module." This would give the solo developer a unified command center that neither tool provides alone. Beads' `bd ready --json` output is already structured for programmatic consumption — Endless could query it across all projects to surface "what's actionable across everything I'm working on."

The concrete recommendation is threefold. First, **don't replicate Beads' task execution features** — the dependency DAG, topological sort, and `bd ready` are well-solved by Beads and would be wasted effort to rebuild. Second, **build the integration layer** — let Endless detect `.beads/` directories in registered projects and surface their task state in the dashboard. Third, **double down on Endless's unique strengths**: cross-project awareness, enforcement hooks, session tracking, document health, and the spawn workflow. These are the features that no tool in the Beads ecosystem provides, and they address problems that Beads' 19,500-star community has not solved.

The "Bitter Lesson" principle from Endless's own design philosophy applies here: rather than building a competing task tracker, provide flexible infrastructure that integrates with whatever task tracker the AI ecosystem converges on. Right now, that's Beads. Endless should be the awareness layer on top of it.

---

## Addendum: Discussion outcomes and design decisions (April 14, 2026)

The following captures conclusions reached during review of this analysis, correcting several points where the original report was imprecise, contradictory, or misaligned with Endless's actual architecture.

### Positioning: Endless is the cockpit, Beads is an optional instrument

Endless and Beads are not competitors and do not overlap as much as a surface-level reading might suggest. The overlapping architectures co-exist as layers. Endless is the outer layer providing cross-project human awareness; Beads is a per-project agent execution layer. A project can use Endless alone, Beads alone, or both. When both are present, Endless reads Beads data for dashboard enrichment but does not sync its plan tree with Beads' issue graph. The two data models coexist as parallel views — one human-facing (Endless plans), one agent-facing (Beads issues). Bidirectional sync between them would be a fragile nightmare and is explicitly ruled out.

Beads integration is a post-MVP enhancement, not a foundational requirement. When built, the simplest version is: detect `.beads/` directories during project registration, flag those projects as Beads-enabled, and shell out to `bd ready --json` to surface task counts on the dashboard. This is a thin read-only integration that adds value without creating dependency.

### Contradiction resolved: the plan tree IS the DAG, and it's enough

The original report simultaneously recommended implementing `endless plan ready` (modeled on Beads' `bd ready`) and cautioned against replicating Beads' dependency DAG. These two recommendations were in tension. The resolution: Endless's parent-child plan tree is already a directed acyclic graph — a tree is a DAG. The difference is that Beads has five dependency types (blocks, parent-child, related, discovered-from, conditional-blocks) while Endless has only parent-child. For a solo developer with human judgment about priority, the full Beads dependency model is unnecessary overhead. `endless plan ready` should surface leaf-level plans with no incomplete children, sorted by whatever heuristic is useful (staleness, priority, recency), but the human decides what's next. No topological sort required.

The decision to start with simple parent-child relationships and let AI usage patterns pave cowpaths for richer dependency types is consistent with Endless's "Bitter Lesson" design principle and avoids the significant engineering burden Beads has taken on maintaining its dependency resolution machinery.

### `endless plan prompt` already solves context injection — `plan brief` extends it

The original report proposed an `endless context` command for injecting project context into AI sessions. Endless already has this capability via `endless plan spawn`, which injects a call to `endless plan prompt <plan_id>`. The existing `plan prompt` outputs task-level instructions written by a prior AI session — the specific directive for the spawned session to follow.

A new `plan brief` command would extend this by also including surrounding context: project identity, sibling plans, parent plan state, relevant documents, activity recency, and document health flags. The distinction between the two commands is valuable because many spawned sessions work on isolated components that need only the prompt (a self-contained task description and example use-case), while others need broader orientation. Making the richer context opt-in via `plan brief` preserves simplicity for the common case.

More broadly, anything visible on the web dashboard should also be accessible via the CLI, so `plan brief` is architecturally consistent. Whether the spawning workflow *requires* it is a separate question — and the answer is no. It's a convenience for sessions that benefit from broader context, not a mandatory part of the spawn flow.

### Integer IDs are the right choice for Endless

The original report suggested hash-based IDs to prevent collisions in multi-agent workflows. This recommendation was wrong for Endless's architecture. Endless uses a single centralized SQLite database where auto-incrementing integers cannot collide. The human ergonomics argument is decisive: "work on plan 123" is something a human can say to an AI without looking anything up, while "work on plan a7f3b2c" requires referencing a list. Notably, Beads itself offers a counter mode (`bd config set issue_id_mode counter`) for projects that prefer sequential integer IDs, acknowledging that hash-based IDs aren't universally better.

If Endless ever needs cross-database stability for collaboration scenarios, a secondary UUID column can be added without replacing the integer primary key. The integer remains the human-facing identifier.

### Reimport is the wrong abstraction — two document tiers instead

The reimport problem (matching file content to DB records via content hashing) has proven to be over-engineered in practice, producing dashboards that are data-rich but insight-poor. Rather than solving reimport, Endless should recognize two distinct categories of content with different data flow directions.

**Plans** are structured records owned by the DB. They flow outward: created via `endless plan create`, modified via CLI or dashboard, and optionally exported to files for reading. The DB is authoritative. When an AI agent works within a spawned session, it works on the plan it was given; changes to the plan go back through the CLI.

**Project documents** (READMEs, design docs, research docs, ADRs, AI-generated markdown) are files owned by the filesystem. They flow inward: Endless discovers them via directory scanning, indexes metadata (path, type, modification timestamp, extracted constraints), and monitors them for staleness — but never tries to own or sync their content. The DB stores metadata about the document, not the document itself.

However, there is an important caveat to making the DB fully authoritative for plans: AI agents think in markdown files. They will naturally create and modify markdown documents during work sessions, sometimes producing plan-like content that exists only as files. Forcing users to only create plans through the CLI would fight against the grain of how AI-assisted development actually works. The adoption strategy requires letting users work with markdown files naturally at first, with Endless discovering and indexing those files, while gradually introducing the structured plan workflow as users become comfortable.

The specific problem of syncing new and updated PLAN documents with the DB remains an open design question. The strategy is to dogfood Endless by using it to build Endless, letting real usage reveal the right sync mechanism rather than designing it speculatively.

### Hook monitors for document health are the right approach

Rather than periodic scanning to detect document drift, Endless's planned hook monitors (ZSH prompt hooks, Claude Code event hooks) should detect in real-time when project documents may need updating. The signal is: "this README was last modified before the plan it documents was changed." Catching drift at the moment it happens prevents the accumulation of stale documents that characterizes the "document entropy" problem. This real-time monitoring capability has no equivalent in Beads, which explicitly delegates document management outside its scope.

### The `source` field: schema now, UI later

Adding an optional `source` field to plans (values like `planned`, `discovered`, `imported`) would distinguish intentionally decomposed child plans from plans that emerged unexpectedly during work, without adding structural complexity. This is metadata on an existing record, not a new relationship type. Since the current assessment is that this distinction isn't clearly valuable yet, the recommendation is to add the column to the schema now (trivial cost, keeps the door open) but not surface it in the CLI or dashboard until usage patterns demonstrate its worth.

### Completed plans: hidden by default

The dashboard should hide completed plans by default, with a "show completed" toggle available on demand. This is a UX signal that reinforces Endless's core value proposition: showing the developer what needs attention right now. Beads' compaction system (summarizing old closed tasks to save agent context window space) is unnecessary for Endless because spawned sessions receive only their specific plan prompt, not the full plan tree. The AI doesn't need to process completed tasks at all.

### Collaboration is a future concern, not a current blocker

The original report noted that SQLite doesn't support collaboration. This is a real limitation worth keeping in mind but not an MVP-blocking one. Endless is designed for a solo developer. If multi-user support becomes necessary, options include LiteFS, rqlite, or a custom sync protocol on top of the existing SQLite schema. Adopting Dolt (which Beads migrated to) would add version-controlled database semantics but also significant operational complexity — the Beads troubleshooting docs describe port conflicts, circuit breakers, shadow databases, and zombie processes. SQLite's operational simplicity is an asset for Endless's target user. Dolt can run in embedded mode without a server, but the full Dolt stack is heavier than SQLite for a single-user tool.

A planned approach of using rsync and a git repo triggered by prompt and hook monitors to track every change — while using SQLite as the primary store — may offer a pragmatic middle ground: version history and recoverability without the operational weight of Dolt.

### Dolt technical details

For reference: Dolt can run in embedded mode (in-process, no server) similar to SQLite, or in server mode for concurrent multi-writer scenarios. The default is embedded. However, the operational complexity is notably higher than SQLite — Beads' own troubleshooting documentation describes issues with port conflicts between multiple Dolt instances, circuit breaker state files, shadow databases created when servers restart on different ports, and zombie processes from improper shutdown. Beads originally used SQLite and migrated fully to Dolt, removing all SQLite infrastructure. This was driven by Beads' need for distributed sync and cell-level merge for multi-agent workflows — requirements Endless does not currently have.

### Summary of revised recommendations

Post-discussion, the original report's recommendations are revised as follows. Keep integer IDs (they're right for centralized single-user architecture). Keep the simple plan tree without adding richer dependency types upfront (let cowpaths emerge). Abandon the reimport approach in favor of two-tier document handling (DB-authoritative plans, filesystem-authoritative project documents). Implement `plan brief` as an extension of the existing `plan prompt` mechanism for sessions that need broader context. Add a `source` column to the plans schema but don't surface it in UI yet. Default to hiding completed plans on the dashboard. Implement real-time document health monitoring via the existing hook infrastructure. Pursue Beads integration as a post-MVP read-only dashboard enhancement, never requiring Beads as a dependency. Continue dogfooding Endless on itself to discover the right plan-file sync strategy organically.
