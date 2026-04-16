# Design: Plan Hierarchy, Enforcement, and Research Integrity

*Captured from design conversation 2026-04-12. This document itself represents a goal under "Implement Endless" — its implementation plan will be a child of that goal.*

---

## The Core Problem

Endless needs to know what the user is working on at all times, at the right level of abstraction. The user thinks in goals ("Dashboard redesign") not implementation steps ("Add active_task_id column"). Additionally, research documents that inform plans become stale as plans evolve, and nobody notices until decisions contradict outdated constraints.

## What's Built

- Hook enforcement blocks Write/Edit without an active session
- `endless plan start/complete/chat` manage sessions
- `ai_sessions` table tracks state with `active_task_id` (needs rename to `active_goal_id`)
- SessionStart context injection tells Claude about the plan
- Plan import from markdown (flat — no hierarchy)
- Status page with master-detail layout
- Dashboard with compact Recent Projects widget

## What's Broken or Missing

- Plan items are flat — enforcement presents 35 granular steps
- No hierarchy despite schema supporting `parent_item_id`
- Import flattens everything; `--clear` destroys parent plans
- `plan chat` is a loophole — Claude WILL take the path of least resistance and self-select `plan chat` instead of asking the user, unless explicitly disallowed
- Research documents become stale as plans evolve
- No connection between research constraints and plan goals

---

## Plan Hierarchy

### The Tree

Plans are trees with unlimited depth. Any item can become a branch when children are added.

```
Project: endless
  └─ Implement Endless (top-level goal)
       ├─ Create Endless Website
       │    ├─ Dashboard Redesign
       │    │    ├─ Status Page Master-Detail
       │    │    └─ Dashboard Homepage Widgets
       │    └─ Work Session Enforcement
       │         └─ Hierarchical Plans ← created mid-work when user zoomed in
       ├─ Create Endless CLI
       └─ Plan Hierarchy & Research Integrity ← THIS design conversation
```

### Branch vs Leaf

- **Branch** (goal/sub-goal): has children. Status derived from children's progress. Progress = completed leaves / total leaves (recursive).
- **Leaf** (step): no children. Status managed directly (pending → in_progress → completed).
- Any leaf becomes a branch when children are added underneath it — this happens naturally when the user says "let's make a more specific plan for this."

### How the Tree Grows Organically

Plans are NOT designed upfront as a neat hierarchy. They evolve:

1. User has a broad goal ("Dashboard redesign")
2. Claude enters plan mode, writes a plan with steps
3. Hook imports steps as children of that goal
4. User realizes a step is too complex: "Whoa, let's make a sub-plan for this"
5. Claude enters plan mode, writes a more specific plan
6. Hook imports THAT as children of the step (which is now a sub-goal)
7. Original parent plan is untouched — still there, still tracked

**Key insight**: It's the USER who decides to zoom in, not Claude. The user hits the brakes, creates focus, and needs to not lose the broader context.

### Top-Level Goals

The top-level should be "Implement [Project]" — not tactical goals like "Dashboard redesign." A design doc, research doc, or plan can all be the genesis of a branch. They're all "here's what we need to do" at different levels of specificity.

---

## Plan Sources

Plans come from multiple sources:

1. **Claude Code plan mode** (`~/.claude/plans/*.md`) — auto-detected by PostToolUse hook
2. **Claude Desktop / Claude Web** — user copies research or plan text
3. **Manual creation** — user writes PLAN.md or runs `endless plan add`
4. **Research that becomes a plan** — user has research output, wants to derive actionable items
5. **Any other AI** — not using Endless, but producing plans the user wants to track

### Import Interface

`endless plan import` is the universal entry point:

```
endless plan import plan.md --project endless                     # root level
endless plan import plan.md --project endless --parent 42         # child of goal #42
endless plan import plan.md --project endless --under "Dashboard" # child by name
```

### Auto-Import Timing

**Decided**: Import happens on ExitPlanMode, not on every plan file save. During plan mode, Claude is iterating on the plan file — importing mid-authoring would be premature and could interrupt the flow. ExitPlanMode means the user approved the plan; that's the right moment to import.

If no active goal exists at ExitPlanMode time, the hook should ask the user where to attach the new plan items (root level, or under an existing goal).

### Markdown → Hierarchy Mapping

```markdown
# Top Heading          → root-level goal (or child of --parent)
## Sub Heading         → child goal
### Sub-sub Heading    → grandchild goal
- Bullet               → step (leaf) under nearest heading
  - Nested bullet      → child step under parent bullet
```

### DB as Primary, Files as Input

**Decided**: Plan files are imported once and then the DB is the source of truth. However:

- For Claude Code plans (`~/.claude/plans/*.md`): the hook knows the file path and can detect changes, comparing with DB to decide on reimport
- For plans from Claude Desktop, Claude Web, or other AIs: the user imports manually via `endless plan import`. These sources don't use Endless, so there's no automatic sync — the user brings the content in
- Context may be lost when importing from external sources, but this is no worse than the existing losses without Endless
- Remove `--clear` flag or change to `--replace` with scoped behavior (only replaces items from same source under same parent)

---

## Stable IDs

### Problem

Reimporting a plan file after edits destroys status. Items that were `completed` get re-inserted as `pending`.

### Solution

Each item gets a `stable_id` column in the DB — a short hash of its content + parent context. On reimport, items are matched by stable_id to preserve status.

```
stable_id = sha256(item_text + parent_stable_id)[:8]
```

### Matching on Reimport

1. Parse new file, compute stable IDs
2. Match against existing items by stable_id
3. **Matched**: update text content, PRESERVE status
4. **New** (no stable_id match): insert as `pending`
5. **Missing from new file**: mark as `removed` (not deleted — user might want it back)

### Handling Significant Rewrites

When a user significantly rewrites an item's text, the hash changes. The existing item appears "deleted" and the rewrite appears "new." Reconciliation strategy:

**Decided**: Start with title-match + exact-match. Add fuzzy matching later if needed.

1. **Exact stable_id match**: hash matches → same item, update text, preserve status
2. **Title match fallback**: stable_id doesn't match but title is unchanged → likely a rewrite, preserve status
3. **Unmatched**: ask user to reconcile orphaned deletions with unmatched additions, or accept delete+new
4. This is acceptable because it's no worse than current behavior (total loss on reimport)

### No Markers in User Files

Stable IDs live in the DB only. NO embedded markers (HTML comments, frontmatter) in the user's markdown files. The mapping between file content and DB records is maintained by Endless internally — either via `source_file` + position tracking in the DB, or via `.endless/plan-map.json`.

**Rationale**: Users edit markdown in text editors where HTML comments are visible and annoying. Annotations in user files would be an adoption dealbreaker. They are only invisible when rendered as HTML, not in a text editor which is how most people read and author markdown.

---

## Enforcement Flow

### Rename: `active_task_id` → `active_goal_id`

The session tracks which goal (branch) the user chose, not which step (leaf).

### The Workflow

1. **SessionStart** → inject goal tree + directive:
   - If active goal exists: show goal name + next 3 steps under it
   - If no active goal: "Ask the user which goal to work on. Present the tree and let them navigate/pick."

2. **User picks a goal** → Claude runs `endless plan start <goal_id>`

3. **PreToolUse on Write/Edit** → check session:
   - Active session with goal → allow
   - No session → BLOCK, show goal tree, tell Claude to ASK THE USER

4. **Claude works through steps** under the goal:
   - `endless plan complete <step_id>` marks step done
   - All steps done → prompt user to confirm goal completion (user may have additional work in mind not yet in the plan)

5. **User zooms in** → enters plan mode, writes new plan
   - On ExitPlanMode: hook imports as children of active goal
   - Active goal shifts to the new sub-goal

6. **"Just chat" — User only, not Claude**:
   - `plan chat` via Claude's Bash tool is BLOCKED (hook detects tool_name="Bash")
   - User must run `! endless plan chat` directly in their terminal (bypasses hooks entirely)
   - This closes the path-of-least-resistance loophole: Claude literally cannot invoke it

### Block Message Shows Navigable Tree

The block message shows the goal tree so Claude can present it to the user. For deep trees, show 2-3 levels with a note to use `endless plan show` for the full tree — this keeps the block message compact and avoids eating Claude's context window. Exact depth limit to be determined when we see real tree sizes.

```
BLOCKED: No active goal for project 'endless'.
Ask the user which goal to work on:

  Implement Endless
    > Create Endless Website
      > Dashboard Redesign (3/12 done)
      > Work Session Enforcement (8/10 done)
    > Create Endless CLI (0/4 done)
    > Plan Hierarchy & Research Integrity (0/? done)

Run `endless plan start <id>` with the goal they choose.
Run `endless plan show` for the full goal tree.
If the user wants to chat without a goal, they can run: ! endless plan chat
(Note: only the user can start a chat session, not you.)
```

### Goal Completion Requires User Confirmation

**Decided**: When all children of a goal complete, DO NOT auto-complete the goal. Prompt the user: "All steps under [goal] are done. Mark it complete, or do you have more work in mind?"

Rationale: The user may have additional work they haven't gotten into the plan yet. Auto-completing would prematurely close the goal.

### Multiple Active Goals Across Sessions

Users MUST be able to work on multiple goals simultaneously across different sessions. Each terminal/session has its own `active_goal_id`. Orchestrating agents that spawn sub-agents are effectively the user — if the user parcels out parallel tasks, multiple goals are active concurrently.

---

## Research Integrity — Constraint Tracking

### The Problem

Research documents contain decisions, constraints, and strategic context. Plans are derived from that context. As plans evolve organically, research docs silently become partially obsolete. Nobody notices until Claude makes a decision that contradicts an outdated constraint.

The user's core pain: "Lots of documents that are partially obsolete, and I have to repeatedly ask Claude to review and update them, which requires HUGE token usage, and Claude rarely does it thoroughly anyway."

### Examples of Research Documents

From the user's projects:
- Business model canvases with strategic decisions (mypurchasehistory)
- Architecture documents with design constraints (homelab)
- Testing strategy documents with tool recommendations (go-tealeaves)
- Competitive analyses with positioning decisions (h2pp)
- Backup strategy with clear scope boundaries (homelab)

These are structured prose with headings — NOT task lists. They need interpretation to extract actionable work.

### Constraint Extraction — AI-Mediated

**Decided**: Constraint extraction is AI-driven, mediated by Claude. The user points Claude at a research doc, Claude extracts key decisions and constraints as compact bullet points, the user reviews and confirms, and the constraints are stored in the DB linked to the goals they inform.

```
Research: backup-strategy.md
  Constraint: "Backup is always free. Verification is premium."
  Constraint: "Orchestrate, don't implement. Wrap proven engines."
  Constraint: "Git is operational workflow, not backup."
  Linked to: goal "Implement Backup System"
```

### Staleness Detection — Dashboard First

**Decided**: Start with the Dashboard as the notification surface for stale constraints. When any step under a linked goal is modified or completed, the Dashboard flags: "Review 3 constraints from backup-strategy.md — still valid?"

Expand to other surfaces (SessionStart injection, CLI) later if Dashboard alone is not sufficient.

### The North Star Principle

Research documents must serve as a "north star" AND must not be allowed to become out of date as plans evolve. The system should:
1. Track which constraints from which documents inform which goals
2. Flag when plans evolve in ways that might contradict or obsolete constraints
3. Make it cheap to review (bullet points, not full doc re-reads)
4. Prevent Claude from making decisions that ignore research context

### Research → Plan Workflow

Deferred for separate design session. Key question: how does research output become actionable plan items? Needs examples and experimentation before committing to an approach.

---

## Open Questions

1. **Goal tree depth in block message**: Exact cutoff (2 levels? 3?) to be determined when we see real tree sizes. Start generous, trim if context usage becomes a problem.

2. **Constraint schema**: DB table design deferred to implementation — likely: source_doc, constraint_text, linked_goal_id, extracted_at, last_reviewed, is_stale.

---

## Implementation Priority

Get Endless usable ASAP:

1. **Hierarchical import** — headings become parents, bullets become children
2. **Rename `active_task_id` → `active_goal_id`** + goal-level start/complete
3. **Goal tree in enforcement/injection** — user sees goals not steps
4. **Stable IDs in DB** (no file markers) — enables non-destructive reimport
5. **Import `--parent`** — enables organic tree growth; import on ExitPlanMode
6. **Goal tree in Status page** — dashboard shows the right level
7. **`plan chat` blocked from Claude's Bash** — only user can invoke directly
8. **Goal completion requires user confirmation**
9. **Constraint extraction from research** — AI-mediated via Claude
10. **Staleness detection on Dashboard** — flag constraints for review when plans change
