# Endless Awareness System — Design Document

## The Vision

A solo developer with 40+ parallel projects needs continuous awareness of what exists, what changed, what's stale, and what needs attention — across all projects simultaneously. Endless is the system that maintains this awareness.

The primary interface is a **web dashboard** that shows three sections on the home page, each linking to a richer view:

1. **Action Queue** — things needing the developer's attention right now
2. **Project Overview** — all projects with status, last activity, active sessions
3. **Session Monitor** — what each Claude/Codex session is doing, which need input

---

## Document Conventions

**STATUS: DRAFT — DO NOT FORMALIZE YET.**

### Design Principle: Progressive Enhancement

Endless must work with any project, regardless of its document conventions. A user should be able to register an existing project with zero changes and get value immediately (activity tracking, file change detection, project listing).

**Tier 0 — Zero convention (always works):**
- Activity tracking via hooks
- File change detection
- Project listing, notes, basic status

**Tier 1 — Recognized filenames (automatic, no user action):**
- Endless recognizes common names (README.md, PLAN.md, etc.) and classifies them
- Works with whatever naming the project already uses
- No required structure inside the files

**Tier 2 — Adopted conventions (opt-in, more value):**
- User adopts standardized names and section structure
- Enables action item parsing from PLAN.md ("Needs Testing", "Needs Review")
- Enables cross-project action queue on the dashboard
- Enables staleness detection between related docs

**Tier 3 — Full integration (maximum value):**
- Dependency rules in `.endless/config.json`
- Claude skill that maintains docs in the standard format
- Automated document lifecycle management

Users should be able to start at Tier 0 and move up as they find Endless useful. Nothing should be blocked by not following conventions.

### Streamlined Document Model

**Goal:** Minimize the number of workflow docs a developer must maintain. History and backlog live in the Endless DB, not in files that bloat Claude's context.

#### Workflow Docs (actively maintained)

| Document | Purpose | Managed By |
|----------|---------|------------|
| `PLAN.md` | Active work: Now + Next sections only | Developer + Claude. Kept under ~100 lines. Completed items archived to Endless DB automatically. |
| `LESSONS.md` | Lessons learned, indexed by keyword | Developer + Claude. Can grow. Endless indexes keywords for targeted lookup. |

#### Plan Management: Endless as Plan Manager

Instead of PLAN.md being the source of truth, **Endless is the plan manager.** The DB holds the structured plan; files are inputs and outputs, not the source of truth.

**Flow:**
1. Claude creates a plan in `~/.claude/plans/` (its natural behavior)
2. Endless imports the plan into the DB — extracting tasks, phases, status
3. Claude queries Endless for current task + context (not a giant file)
4. Endless feeds Claude only the relevant slice
5. Dashboard shows the full structured plan to the user
6. Endless can generate a lean PLAN.md from the DB when needed (for other tools, for humans, for sessions without Endless)

**Progressive enhancement:**
- Tier 0: Claude works normally, Endless passively indexes `~/.claude/plans/` files
- Tier 1: Endless surfaces structured views, Claude starts querying Endless
- Tier 2: Claude asks "what's my current task?", works on it, reports "done", Endless advances the plan

**Key benefits:**
- Claude's context only gets the current task + immediate context, not the full history
- The user sees the full plan on the dashboard, structured and searchable
- Completed items archive to the DB automatically
- Deferred/future items stored in DB, queryable via dashboard/CLI
- PLAN.md is an optional output, not a required input
- Works regardless of what Claude names its plan files

#### Reference Docs (read as needed, not workflow)

| Document | Purpose | Notes |
|----------|---------|-------|
| `README.md` | Project overview | Standard. |
| `CLAUDE.md` | AI coding context | Instructions for Claude. |
| `DESIGN_BRIEF.md` | Design intent | Optional. The "why" before implementation. |
| `CHANGELOG.md` | Release history | Optional. |

#### Plural Document Types (inherently multiple)

| Directory | Purpose |
|-----------|---------|
| `docs/adrs/` | Architecture Decision Records. One per decision. |
| `docs/research/` | Research artifacts. Named `<type>-<date>-<topic>.md` |
| `docs/design/` | Design documents. Named `design-<date>-<topic>.md` |

### Document Naming Convention for Dated Artifacts

```
<type>-<YYYY-MM-DD>-<topic-slug>.md
```

Examples:
- `design-2026-04-09-awareness-system.md`
- `research-2026-03-27-homelab-apps-potential.md`
- `brief-2026-04-02-endless-design.md`

### PLAN.md Structure Convention

```markdown
# Project Name — Plan

## Needs Manual Testing
- item 1
- item 2

## Needs Review
- item 1

## In Progress
- item being worked on

## Upcoming
- next items to implement

## Blocked
- items waiting on something
```

Endless parses these sections to populate the Action Queue on the dashboard. Section names are configurable but the above are defaults.

### TODO.md Structure Convention

```markdown
# Project Name — Future Work

## Feature Name
Description of the feature. Reference design docs if applicable.

## Another Feature
Description.
```

Each `##` section is one future work item. Endless indexes these for cross-project search and dashboard display.

---

## Data Collection

### Sources of Truth

| Data | Source | Collection Method |
|------|--------|-------------------|
| Project existence | `.endless/config.json` on disk | Reconciliation on every command |
| Project metadata | `.endless/config.json` | Reconciliation |
| File changes | Filesystem mtime | `endless-hook prompt` (ZSH precmd) |
| Claude activity | Hook payload JSON | `endless-hook claude` (Claude Code hook) |
| Codex activity | Hook payload JSON | `endless-hook codex` (Codex hook) |
| tmux context | `tmux display-message` | `endless-hook prompt` |
| Document state | PLAN.md, TODO.md, etc. | Parsing on scan or hook trigger |
| Manual notes | `endless note add` | Direct DB write |
| Action items | Parsed from PLAN.md sections | Document indexing |

### What Gets Stored in the DB

**`activity` table** — every hook trigger (prompt, claude, codex) with:
- project_id, source, working_dir, session_context (tmux UUIDs or AI session_id), timestamp

**`file_changes` table** — detected file changes:
- project_id, relative_path, change_type (new/modified/deleted), detected_at, source

**`documents` table** — tracked markdown files:
- project_id, relative_path, doc_type, content_hash, size, last_modified

**`notes` table** — both system-generated and manual:
- project_id, note_type, message, source, target_doc, resolved status

**`action_items` table** (NEW — to be created):
- project_id, section (e.g., "needs_testing", "needs_review", "in_progress"), text, source_doc, line_number, created_at, resolved_at

### What Gets Stored on Disk

- `.endless/config.json` — project registration, metadata, watch patterns
- `.endless/config.json` with `type: group` — group directory markers
- `~/.config/endless/config.json` — global config (roots, ignore, ownership)
- `~/.config/endless/endless.db` — all indexed data (cache, rebuildable from disk)

---

## Claude Session Tracking

### How We Know What a Session Is Working On

1. **`cwd` from hook payload** — maps session to project
2. **`transcript_path` from Notification hooks** — points to the full conversation JSONL
3. **PLAN.md parsing** — the "In Progress" section tells us what's active
4. **Tool use patterns** — PostToolUse events show what files Claude is touching
5. **Session start/end** — bracket the work period

### Session States (for dashboard)

| State | How Detected |
|-------|-------------|
| **Working** | Recent PreToolUse/PostToolUse events (last 30 seconds) |
| **Idle** | Notification:idle_prompt received, no subsequent activity |
| **Needs Input** | Notification:permission_prompt received |
| **Ended** | SessionEnd event or no activity for >10 minutes |

### What the Dashboard Shows Per Session

- Project name and path
- Session ID (linked to transcript)
- Current state (working/idle/needs input/ended)
- Last tool used
- Duration
- Files modified this session

---

## Dashboard Design

### Home Page: Three Sections

#### 1. Action Queue
Cross-project list of things needing developer attention, ordered by urgency:

- **Needs Input** — Claude sessions waiting for permission or response (highest urgency)
- **Needs Testing** — items from PLAN.md "Needs Manual Testing" sections
- **Needs Review** — items from PLAN.md "Needs Review" sections
- **Stale Documents** — docs whose dependencies changed (from staleness detection)

Each item links to the project detail view.

#### 2. Project Overview
Table of all active projects showing:

- Name, label, status, language
- Last activity (from activity table)
- Pending notes count
- Active sessions count
- Progress indicator (from PLAN.md parsing)

Sortable by any column. Click to drill into project detail.

#### 3. Session Monitor
Active Claude/Codex sessions:

- Project, session state (working/idle/needs input)
- Duration, last event
- Files modified count

---

## Implementation Order

### Phase A — Document Conventions + Parsing
1. Formalize conventions (this document)
2. Build PLAN.md / TODO.md parser that extracts section items
3. Create `action_items` table
4. `endless actions [<name>]` command — show action items from PLAN.md

### Phase B — Dashboard Skeleton
5. Choose web framework (likely Astro or Go+Templ+HTMX — needs evaluation)
6. Build home page with three sections using existing DB data
7. `endless serve` command to start local server

### Phase C — Rich Dashboard
8. Project detail view
9. Session monitor (real-time-ish via polling)
10. Action queue with cross-project aggregation
11. PLAN.md editing from dashboard (stretch)

### Phase D — Claude Integration
12. Claude skill that updates PLAN.md in the standardized format
13. Skill that reads Endless action queue and prioritizes work
14. Auto-capture of Claude Desktop research downloads into project docs

---

## Open Questions

1. **Dashboard tech**: Astro (static-ish, good DX) vs Go+Templ+HTMX (matches eventual Go port) vs something else?
2. **PLAN.md section names**: Are the defaults above right? Should they be configurable per-project?
3. **Cross-session coordination**: When multiple Claude sessions touch the same project, how do we show that?
4. **Research artifact capture**: How to standardize the flow from "Claude Desktop conversation → downloaded doc → project's docs/ directory"?
5. **Notification routing**: Should idle_prompt/permission_prompt trigger OS-level notifications (e.g., macOS Notification Center)?
