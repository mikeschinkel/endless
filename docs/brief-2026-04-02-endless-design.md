# Endless — Revised Design Brief

## The Problem, Restated

A solo developer with 40 years of experience and 10+ parallel projects is drowning. The productivity gains from AI coding assistants (Claude Code, Claude Chat, Cowork, ChatGPT, Codex) have enabled genuine multi-project parallelism for the first time — but no tool exists to maintain awareness across all of them. The consequences are concrete and demonstrated:

- **Forgotten projects.** Vigil (a file-watching daemon) and Yggdrasil (an Obsidian-Git bridge) were both designed and then forgotten until this conversation surfaced them. A project tracker would have prevented this.
- **Lost conversations.** The Yggdrasil design brief existed only in a ChatGPT conversation that was never captured to the filesystem. Without monitoring and classifying AI chats by project, work products vanish.
- **Document entropy.** Each project accumulates 5–20+ markdown files (plans, research reports, ADRs, design briefs, READMEs). These proliferate, become stale relative to each other, and require expensive periodic full-review sessions with Claude that miss important things because the context is overwhelming.
- **Session confusion.** tmux sessions run for days, sometimes spanning multiple projects, and it becomes unclear which session serves which purpose.
- **Git is insufficient.** Even with perfect commit discipline, `git status` provides only a fraction of the insight needed. It knows nothing about document dependencies, project-level status, which AI conversations relate to which project, or which tmux session is doing what. The tool must maintain its own state that goes far beyond what git can infer.

The tool needed is not a task manager, not an agent orchestrator, and not a git scanner. It is a **project awareness system** — a daemon-backed dashboard that continuously and cheaply maintains knowledge of what exists, what has changed, what is stale, and what matters right now, across all projects simultaneously.

---

## Name

**Endless** — evokes the vast potential and the never-ending nature of a developer's project portfolio. The only conflicts found are movie titles. Works well as a CLI command: `endless list`, `endless status`, `endless scan`. Short, memorable, brandable, no conflicts in developer tooling or Homebrew.

### Runner-up candidates worth holding

orbit, ops, pulse, beacon, dossier — all clear in Homebrew. "ops" has SEO challenges. "pulse" has naming conflicts in several tech domains (ALM tool, LinkedIn app, interbank network). "dossier" is evocative of intelligence-gathering but at 7 characters is longer than ideal for frequent CLI use.

---

## What This Tool Is

A daemon-backed project awareness system with a web dashboard. It continuously monitors `~/Projects` (and configured subdirectories), maintains a SQLite database of project state, tracks document lifecycle and inter-document dependencies, and surfaces what matters through a browser-based dashboard with periodic background scanning.

## What This Tool Is Not

- Not an agent orchestrator (h2, Scion, claude-squad handle that)
- Not a task manager (tasks exist within projects, but the tool tracks projects)
- Not a git workflow tool (the developer's own git TUI handles commits)
- Not an MCP server (CLI is the interface for AI coding tools)
- Not a context file manager (CLAUDE.md, AGENTS.md are per-project concerns)

---

## Core Concepts

### Projects

A project is a directory in `~/Projects/` or a subdirectory of a grouping directory (e.g., `~/Projects/go-pkgs/go-tealeaves`). Grouping directories are identified by a marker file that flags them as containers rather than projects. This could be either `.endless/config.json` where `project_group` or `group` is `true`, or maybe `project` is false?  Or if we need a file that does not need to be introspected maybe we could have a `.endless/type=group` file that does exist?

Projects are explicitly registered by placing a `.endless/config.json` file in the project root — this is not about discovering other people's projects but tracking your own.

### Documents

Markdown files within a project that Claude generates or the developer creates: design briefs, plans, research reports, ADRs, READMEs, changelogs. These are the primary unit of "knowledge" the tool manages. Each document has a type, a staleness state, and declared dependencies on other documents or on categories of code changes.

### Project Dependencies

Projects depend on other projects (e.g., go-tealeaves depends on bubbletea, vigil depends on go-gitutils). These dependencies are explicitly declared in `.endless/config.json` and tracked in the database. Changes in a dependency project can flag dependent projects as potentially needing attention.

### Private Companions

Many projects generate files that should not be committed to public repos — research reports, competitive analyses, internal notes, design explorations. Endless manages a private companion system with a strict **zero-leakage principle**: not only must private files be excluded from the public repo, but the *fact that files were made private* must also be invisible. Even metadata about what was privatized is a leak — by analogy, even if you can't hear a phone call's audio, knowing the call happened between two parties reveals information you may not want revealed.

The mechanism: a `.endless/private` manifest file (same format as `.gitignore`) lists file patterns to track in a parallel private repository. This manifest is excluded from the public repo via `.git/info/exclude` (not `.gitignore`, which is itself committed and would reveal the existence of private file management). The rest of `.endless/` — including `config.json` — is committed normally so that the project's Endless configuration is visible and discussable.

A background agent periodically reviews changed content against learned privacy criteria to flag files that should be private but aren't listed.

---

## Architecture Overview

### Phase 1 — Shell Scripts + SQLite + Web Dashboard

The MVP is implemented as:

- **`endlessd`** — A bash daemon that runs on a timer (e.g., every 5 minutes via launchd or a background loop), scans project directories, and updates the SQLite database.
- **`endless`** — A bash CLI for querying state, registering projects, and managing documents.
- **SQLite database** at `~/.config/endless/endless.db` — the single source of truth, queried via `sqlite3` CLI.
- **Web dashboard** — A locally-served web application that reads from the SQLite database via a thin API layer. Technology choice (Astro, Go+Templ+HTMX, or other) deferred until Phase 1b — Astro's strength is documentation sites via Starlight, which may not be the right fit for a dashboard. This is the primary visual interface.
- **Browser extension** (later in Phase 1) — Monitors authenticated claude.ai and chatgpt.com sessions, classifies conversations by project, and sends metadata to Endless via API POST to its local server. Architecture modeled on SaveTabs (a prior project that already implements browser-to-local-server communication patterns).

### Phase 2 — Go Port

When the workflow stabilizes, port the daemon and CLI to Go using:

- `go-cliutil` (not Cobra) for CLI parsing
- `go-tealeaves` / Bubbletea for optional TUI
- `modernc.org/sqlite` for database access (pure Go, no CGo)
- Templ + HTMX for the web dashboard (or continue with Astro if it proves sufficient)
- The Go binary replaces both `endlessd` and `endless` as subcommands of a single binary

### Phase 3 — Intelligence Layer

- AI-assisted document classification and staleness detection
- Privacy guardian agent that learns criteria for what should be private
- Cross-project impact analysis (dependency change propagation)
- Suggested actions ("README.md needs updating because PLAN.md changed")

```
┌─────────────────────────────────────────────────────────────┐
│                    Web Dashboard (TBD)                        │
│  Project list │ Status view │ Document map │ Session tracker  │
├─────────────────────────────────────────────────────────────┤
│            API Server (receives extension POSTs)             │
├─────────────────────────────────────────────────────────────┤
│                    CLI Interface (bash/Go)                    │
│  list │ scan │ status │ register │ note │ archive │ serve    │
├─────────────────────────────────────────────────────────────┤
│                    Application Layer                         │
│  Scanner │ Document Tracker │ Session Monitor │ Privacy Guard │
├─────────────────────────────────────────────────────────────┤
│               SQLite Database (~/.config/endless/)           │
│  projects │ documents │ dependencies │ sessions │ notes      │
├──────────────┬──────────────┬───────────────────────────────┤
│  Filesystem  │  Private Git │  Browser Extension            │
│  Scanner     │  Repos       │  (claude.ai / chatgpt.com)    │
│  (~/Projects)│  (rsync +    │                               │
│              │  debounced   │                               │
│              │  commits)    │                               │
└──────────────┴──────────────┴───────────────────────────────┘
```

---

## The Document Lifecycle Epiphany

This is the architectural insight that makes the tool tractable. Instead of periodic expensive full-review sessions where Claude must re-read 5–20 files and try to figure out what matters, the system maintains continuous incremental awareness.

### How It Works

The daemon watches for file changes on a relaxed schedule (once per scan cycle, not real-time). When a document changes, the system records what changed and when. Documents declare dependencies on other documents and on types of changes. When a dependency changes, the system creates a "note" — a lightweight record that something downstream may need updating.

For example:

- `README.md` depends on `PLAN.md` (content dependency) and on `cmd/` directory changes (code dependency).
- When `PLAN.md` is modified, the system notes: "README.md may need updating because PLAN.md changed."
- When files in `cmd/` change significantly, the system notes: "README.md may need updating because the CLI interface changed."
- These notes accumulate in the database. When the developer (or Claude) next opens the project, the notes provide a precise, cheap-to-review list of what needs attention — no full-review required.

### Document Types and Their Lifecycle

The system recognizes document types by convention (filename patterns and directory placement):

- **README.md** — Project overview. Depends on almost everything; rarely the thing that changes first.
- **PLAN.md** — Current implementation plan with phases. The authoritative "what are we doing" document. Single file, structured with phases marked done/undone.
- **DESIGN_BRIEF.md** or `design-briefs/*.md` — Design intent documents. Stable unless the project direction changes.
- **ADRs** (`adrs/*.md`) — Architecture Decision Records. Append-only by convention; never stale, but may become superseded.
- **Research reports** (`research/*.md`) — Point-in-time artifacts. Become stale as the landscape evolves but are valuable as historical records.
- **CHANGELOG.md** — Release history. Depends on completed work.
- **CLAUDE.md** — AI coding context. Depends on architecture and convention changes.

### The Anti-Proliferation Rule

A key structural constraint: plans should be a single `PLAN.md` file with phases, not a proliferating set of numbered plan files. The tool enforces this by convention — one plan file per project, structured as a markdown document with phases that can be marked done. If Claude generates `plan-v2.md` or `implementation-plan-phase-3.md`, the system flags this as document sprawl and suggests consolidation.

Similarly, the tool maintains a single canonical document per type (one README, one PLAN, one DESIGN_BRIEF) unless the document type is inherently plural (ADRs, research reports). This prevents the entropy that comes from Claude creating new files rather than updating existing ones.

### Archiving

When a document becomes obsolete (superseded by another, or representing completed work), it moves to `.endless/archive/` within the project directory. The archive is dated and the move is recorded in the database. This provides a consistent, tool-managed archival mechanism rather than ad-hoc deletion or renaming.

### Claude's Role in Document Maintenance

Rather than requiring the developer to specify all dependency rules upfront, the system allows Claude to learn and improve its tracking over time. The initial rules are simple (README depends on PLAN, PLAN depends on major code changes). As Claude observes patterns — "every time Mike changes the CLI flags, he also updates the README" — it can propose new dependency rules. The system stores these learned rules in the database alongside the explicit ones, with a confidence score and the ability for the developer to confirm or reject them.

The key constraint: the system must track what has changed cheaply (file mtimes, sizes, hashes) so that when Claude is asked to help, it receives a focused diff of what matters rather than an overwhelming dump of everything.

### Dependency Granularity: Files and Regions

Document dependency tracking operates at two levels. **File-level** dependencies are the baseline: "README.md depends on PLAN.md" or "CLAUDE.md depends on changes in `internal/`." These are simple, explicit, and cover most cases.

**Region-level** dependencies provide finer grain where needed. A "region" is a semantically meaningful section within a document — but rather than prescribing what regions are, Claude gets to define and evolve how regions are identified. A region might be a markdown heading section, a fenced code block, a YAML frontmatter field, or any other boundary that Claude determines is useful for tracking dependencies. For instance, Claude might learn that the "## CLI Usage" section of README.md depends on `cmd/` changes while the "## Architecture" section depends on `internal/` structure changes, and only the affected region needs the staleness note.

Over time, emergent behaviors will develop as Claude experiments with different region boundaries and dependency mappings. The system stores region definitions in the database alongside dependency rules, and they settle toward an optimal approach through the same learn-and-confirm cycle as other dependency rules. The initial implementation can start with file-level only and add region tracking once the file-level system is stable.

---

## Private Companion Repositories

### The Problem

Research reports, competitive analyses, pricing strategies, internal notes, and AI-generated exploration documents are valuable but should never appear in public repositories. Currently these files either live in the project directory (risking accidental commit) or don't exist at all (because there's no safe place to put them).

### The Zero-Leakage Principle

Private file management must leak *nothing* to the public repo — not even the fact that privatization is happening. The `.endless/private` manifest is excluded via `.git/info/exclude` (which is never committed) rather than `.gitignore` (which is committed and visible). An observer of the public repo should have no indication that any files are being managed privately. The rest of `.endless/` is committed normally — only the private manifest is hidden.

### The Mechanism

1. An `.endless/private` manifest file (`.gitignore` format) lists file patterns that belong in the private companion (e.g., `research/*.md`, `notes/`, `COMPETITIVE_ANALYSIS.md`).
2. The `.endless/private` file is added to `.git/info/exclude` so it never appears in the public repo and its existence is invisible.
3. The daemon periodically rsyncs matched files to a companion directory structure at a configured location (e.g., `~/.config/endless/private-repos/<project-name>/`).
4. The companion directory is itself a git repository with vigil-style debounced auto-commits, providing version history for private files.
5. Optionally, private companion repos can be pushed to a private GitHub remote (e.g., `github.com/myorg/myrepo-private`).

### Privacy Guardian

A background process (initially a scheduled script, later an AI-assisted agent) periodically reviews recently changed files in project directories against learned privacy criteria. If a file that looks like it should be private (contains API keys, competitive analysis language, pricing data, internal strategy notes) is not listed in `.private`, the system flags it for review. The criteria are stored in the database and evolve as the developer confirms or rejects suggestions.

---

## Session Tracking

### tmux Sessions

The daemon periodically polls `tmux list-sessions` and `tmux list-windows` to capture active sessions, their working directories, and window names. This data maps sessions to projects (via working directory matching against registered projects) and is stored in the database with timestamps. The dashboard shows which sessions are active, which projects they're associated with, and how long they've been running.

When a session spans multiple projects (multiple windows with different working directories), the system records all associations. The dashboard can show "Session `dev-main` is working on: go-tealeaves (window 1), vigil (window 2), endless (window 3)."

### Claude Code Sessions

The daemon scans `~/.claude/` for relevant files — session logs, plans, hook-generated output. These are parsed for project associations (which project directory was Claude Code running in?) and stored in the database. This provides a history of "Claude Code worked on project X at time Y."

### Browser Extension (Later Phase 1)

A browser extension for Chrome/Arc monitors authenticated sessions on claude.ai and chatgpt.com. It captures conversation metadata (title, timestamps, length) and attempts to classify each conversation by project based on content signals (project names mentioned, code snippets referencing known files, explicit project references). The extension communicates with Endless via API POST to its local server — the same server that powers the web dashboard. This architecture is already proven by SaveTabs, a prior project that implements exactly this browser-to-local-server communication pattern. Classifications are stored in the database and surfaced on the dashboard. This directly addresses the Yggdrasil problem — a valuable design conversation with ChatGPT that was never connected to a project and would have been lost.

---

## Database Schema (MVP)

This schema is a potential, but by no means a final decision.

```sql
-- Projects
CREATE TABLE projects (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    path TEXT NOT NULL UNIQUE,
    group_name TEXT,              -- e.g., "go-pkgs" for grouped projects
    description TEXT,
    status TEXT DEFAULT 'active', -- active, paused, archived, idea
    language TEXT,                -- go, bash, typescript
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Project dependencies
CREATE TABLE project_deps (
    project_id INTEGER NOT NULL,
    depends_on_id INTEGER NOT NULL,
    dep_type TEXT DEFAULT 'runtime', -- runtime, dev, tooling
    notes TEXT,
    PRIMARY KEY (project_id, depends_on_id),
    FOREIGN KEY (project_id) REFERENCES projects(id),
    FOREIGN KEY (depends_on_id) REFERENCES projects(id)
);

-- Documents within projects
CREATE TABLE documents (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    relative_path TEXT NOT NULL,
    doc_type TEXT,                -- readme, plan, design_brief, adr, research, changelog, claude_md
    content_hash TEXT,
    size_bytes INTEGER,
    last_modified TEXT,
    last_scanned TEXT,
    is_archived INTEGER DEFAULT 0,
    archived_at TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

-- Document dependency rules (file-level and region-level)
CREATE TABLE doc_dependencies (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    dependent_doc TEXT NOT NULL,  -- relative path or doc_type pattern
    dependent_region TEXT,        -- NULL for file-level; region identifier for region-level
    depends_on TEXT NOT NULL,     -- relative path, doc_type, or directory glob
    dep_kind TEXT NOT NULL,       -- content, code, structural
    learned INTEGER DEFAULT 0,   -- 0 = explicit, 1 = AI-suggested
    confidence REAL DEFAULT 1.0,
    confirmed INTEGER DEFAULT 0, -- developer confirmed learned rule
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

-- Region definitions within documents (Claude-defined, evolving)
CREATE TABLE doc_regions (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    document_path TEXT NOT NULL,
    region_id TEXT NOT NULL,      -- e.g., "section:cli-usage", "heading:## Architecture"
    region_type TEXT NOT NULL,    -- heading, fenced_block, frontmatter_field, custom
    start_marker TEXT,           -- how to find this region (e.g., "## CLI Usage")
    content_hash TEXT,           -- hash of region content at last scan
    last_modified TEXT,
    learned INTEGER DEFAULT 0,
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

-- Notes (the "something needs attention" queue)
CREATE TABLE notes (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    note_type TEXT NOT NULL,      -- staleness, update_needed, sprawl, privacy, general
    message TEXT NOT NULL,
    source TEXT,                  -- what triggered this note
    target_doc TEXT,              -- which document needs attention
    resolved INTEGER DEFAULT 0,
    created_at TEXT NOT NULL,
    resolved_at TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

-- tmux sessions
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY,
    session_name TEXT NOT NULL,
    window_name TEXT,
    working_dir TEXT,
    project_id INTEGER,
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    is_active INTEGER DEFAULT 1,
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

-- AI chat conversations (from browser extension)
CREATE TABLE ai_chats (
    id INTEGER PRIMARY KEY,
    platform TEXT NOT NULL,       -- claude, chatgpt
    chat_id TEXT,                 -- platform-specific identifier
    title TEXT,
    project_id INTEGER,          -- classified project association
    classification_confidence REAL,
    started_at TEXT,
    last_message_at TEXT,
    message_count INTEGER,
    summary TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

-- Claude Code sessions (from ~/.claude scanning)
CREATE TABLE claude_sessions (
    id INTEGER PRIMARY KEY,
    project_id INTEGER,
    session_dir TEXT,
    started_at TEXT,
    last_activity TEXT,
    plan_files TEXT,              -- JSON array of plan file paths
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

-- Scan history (track what the daemon has done)
CREATE TABLE scan_log (
    id INTEGER PRIMARY KEY,
    scan_type TEXT NOT NULL,      -- full, incremental, documents, sessions
    started_at TEXT NOT NULL,
    completed_at TEXT,
    projects_scanned INTEGER,
    changes_detected INTEGER
);

-- Private file tracking
CREATE TABLE private_files (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    relative_path TEXT NOT NULL,
    content_hash TEXT,
    last_synced TEXT,
    companion_repo TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

-- Learned privacy criteria
CREATE TABLE privacy_rules (
    id INTEGER PRIMARY KEY,
    pattern TEXT NOT NULL,        -- glob, keyword, or content pattern
    rule_type TEXT NOT NULL,      -- filename_pattern, content_keyword, directory
    confidence REAL DEFAULT 0.5,
    confirmed INTEGER DEFAULT 0,
    created_at TEXT NOT NULL
);
```

---

## CLI Design (MVP — Bash)

These commands are potential, but by no means a final decision.

```bash
# Project management
endless register [<path>]        # Register current or specified dir as project
endless list                     # List all projects with status summary
endless status [<project>]       # Detailed status of one or all projects
endless archive <project>        # Archive a project
endless deps <project>           # Show dependency graph for a project

# Scanning
endless scan                     # Run a full scan now (normally daemon does this)
endless scan --docs              # Scan only documents
endless scan --sessions          # Scan only tmux/claude sessions

# Document management
endless docs [<project>]         # List documents with staleness indicators
endless notes [<project>]        # Show pending notes (things needing attention)
endless note <project> <message> # Manually add a note
endless resolve <note-id>        # Mark a note as resolved
endless archive-doc <path>       # Archive a document (move to .endless/archive/)

# Dashboard
endless serve                    # Start the web dashboard

# Private companions
endless private init [<project>] # Initialize private companion for a project
endless private sync [<project>] # Sync private files now
endless private check            # Run privacy guardian check

# Daemon
endless daemon start             # Start background scanning daemon
endless daemon stop              # Stop daemon
endless daemon status            # Show daemon status
```

### Project Config File (`.endless/config.json`)

This config is potential, but by no means a final decision.

```json
{
  "name": "vigil",
  "description": "File-watching daemon with auto-commit",
  "language": "go",
  "status": "paused",
  "dependencies": [
    { "project": "go-gitutils", "type": "runtime" },
    { "project": "go-cliutil", "type": "runtime" }
  ],
  "documents": {
    "rules": [
      {
        "dependent": "README.md",
        "depends_on": ["PLAN.md", "cmd/**/*.go"],
        "kind": "content"
      },
      {
        "dependent": "CLAUDE.md",
        "depends_on": ["internal/**/*.go", "PLAN.md"],
        "kind": "content"
      }
    ]
  }
}
```

### Private Manifest (`.endless/private`) — excluded via `.git/info/exclude`

```gitignore
# Research and competitive analysis
research/*.md
COMPETITIVE_ANALYSIS.md

# Internal notes
notes/

# Pricing and strategy
pricing/
```

---

## Vigil Integration

Vigil (the debounced auto-commit daemon) is a natural component of Endless rather than a separate tool. The private companion repository system needs exactly what Vigil provides: rsync files to a shadow directory and auto-commit changes with debouncing. Rather than building Vigil as a standalone tool and then integrating it, Vigil's core functionality (poll → diff → debounce → commit to a separate git-dir) becomes the `endless private sync` subsystem.

This also addresses the broader need: since the developer is deliberately not committing to project repos (because the Git commit TUI isn't ready), Endless can maintain its own version history of project files by running vigil-style shadow commits to `~/.config/endless/repos/<project>/`. This gives Endless the version history it needs for change detection and staleness tracking without depending on the project's own git state.

---

## Yggdrasil Integration (Future)

Yggdrasil (Obsidian ↔ Git bidirectional sync) becomes relevant if Obsidian is chosen as a dashboard or knowledge interface. The Obsidian Projects plugin was discontinued in May 2025, but the underlying concept — database views from markdown frontmatter — is exactly what a markdown-native project tracker needs. If the web dashboard proves insufficient, Yggdrasil could bridge Endless's project markdown files into an Obsidian vault for graph visualization and wiki-linking across projects. This is a Phase 3+ concern.

---

## Web Dashboard Design

The primary visual interface. Implemented as a locally-served web application with an API layer that reads from the SQLite database. Technology choice deferred (see Resolved Decisions #1).

### Dashboard Views

**Portfolio View (default):** All projects in a grid or list, sorted by the developer's chosen priority. Each project card shows: name, status badge (active/paused/archived/idea), language, last activity timestamp, pending notes count, document staleness summary. Filter by status, language, group. Search by name or description.

**Project Detail View:** Single project deep-dive showing: description, status, dependencies (which projects this depends on and which depend on it), all documents with type badges and staleness indicators, pending notes queue, recent tmux sessions, recent Claude Code sessions, associated AI chat conversations, private file status.

**Notes View:** Cross-project queue of everything needing attention. Filterable by project, note type, age. This is the "what should I work on" view — not in a task-management sense, but in a "what has drifted and needs reconciliation" sense.

**Sessions View:** Active tmux sessions mapped to projects. Claude Code session history. AI chat conversations classified by project.

**Dependency Graph:** Visual graph of project-to-project dependencies. Highlights dependency chains (e.g., if go-gitutils changes, vigil and the git-commit TUI are both affected).

### Update Mechanism

The dashboard does not need real-time updates. The daemon runs scans on a schedule (e.g., every 5 minutes for filesystem, every hour for deeper document analysis). The dashboard polls or reloads on a comfortable interval. HTMX-style partial updates or equivalent client-side fetch patterns work well here regardless of framework choice.

---

## Daemon Behavior

The daemon (`endlessd`) runs as a background process, started via `endless daemon start` (which could be a launchd plist on macOS, but Phase 1 just documents this rather than automating it).

### Scan Schedule

- **Every 5 minutes:** Filesystem scan of `~/Projects` — detect new/modified/deleted files, update document records, check mtimes against dependency rules, generate staleness notes.
- **Every 15 minutes:** tmux session scan — map active sessions to projects.
- **Every 15 minutes:** `~/.claude` scan — detect new session artifacts, plans, hook logs.
- **Every 24 hours:** Deep document analysis — review document dependency rules, run privacy guardian check, flag document sprawl.

### What the Daemon Does NOT Do

- It does not commit to project's ~/.git repositories; that's the developer's domain.
- It does not modify project files. (It only reads and records.)
- It does not require real-time filesystem watching. (Polling on a schedule is sufficient and avoids kqueue/fsnotify resource issues.)
- It does not push anything anywhere. (Private companion sync is triggered explicitly or on a relaxed schedule.)

---

## Implementation Roadmap

### Phase 1a — Foundation (Bash + SQLite)

1. Create the SQLite schema and `endless` CLI wrapper for basic queries.
2. Implement `endless register` to create `.endless/config.json` in a project.
3. Implement `endless scan` to walk `~/Projects`, find registered projects, index documents by mtime/size/hash.
4. Implement `endless list` and `endless status` for terminal output.
5. Implement `endless notes` and dependency-based staleness detection.

### Phase 1b — Dashboard (Web)

6. Evaluate dashboard technology (Astro, Go+Templ+HTMX, or other) based on Phase 1a data access patterns.
7. Build the Portfolio View, Project Detail View, and Notes View.
8. Implement `endless serve` to launch the dashboard.

### Phase 1c — Session Tracking

9. Implement tmux session scanning and project association.
10. Implement `~/.claude` scanning for session artifacts.
11. Surface session data in the dashboard.

### Phase 1d — Private Companions

12. Implement `.private` manifest parsing and rsync-based file mirroring.
13. Implement vigil-style debounced auto-commit for companion repos.
14. Implement `endless private` CLI commands.

### Phase 1e — Browser Extension

15. Build a Chrome extension modeled on SaveTabs architecture that monitors claude.ai and chatgpt.com.
16. Implement conversation classification by project.
17. POST chat metadata to Endless server API; surface in the dashboard.

### Phase 2 — Go Port

18. Port CLI to Go using go-cliutil.
19. Port daemon to Go with proper signal handling and launchd integration.
20. Port web dashboard to Templ + HTMX (or keep Astro if it's working well).
21. Add optional Bubbletea TUI using go-tealeaves components.

### Phase 3 — Intelligence

22. AI-assisted document classification and dependency rule learning.
23. Privacy guardian with learned criteria.
24. Cross-project impact analysis.
25. Yggdrasil integration for Obsidian (if desired).

---

## Key Design Decisions

1. **Shell scripts first, Go later.** Getting the workflow right matters more than the implementation language. Bash + sqlite3 + Astro is the fastest path to a usable MVP.
2. **Explicit project registration.** Projects opt-in via `.endless/config.json`. No magic discovery of unregistered directories.
3. **Documents as first-class entities.** Not just files — typed, dependency-tracked, lifecycle-managed artifacts with staleness detection.
4. **Incremental awareness, not periodic review.** The daemon continuously accumulates small observations (notes) rather than requiring expensive full-context sessions.
5. **Private companions as a core concept.** The public/private split is a fundamental need, not an afterthought.
6. **Web dashboard as primary visual interface.** TUI is Phase 2. The browser is where the developer already lives.
7. **No real-time requirements.** Everything operates on relaxed schedules. 5-minute scans, hourly deep analysis, daily privacy checks.
8. **Single source of truth in SQLite.** Every interface (CLI, web, future TUI, future MCP) reads from the same database.
9. **Anti-proliferation by convention.** One PLAN.md, one README.md, one DESIGN_BRIEF.md per project. The tool flags sprawl.
10. **Claude learns, developer confirms.** Dependency rules and privacy criteria start simple and evolve through AI suggestion + human confirmation.

---

## Relationship to Other Projects

| Project | Relationship to Endless |
|---------|------------------------|
| **h2 / h2pp** | Orthogonal. h2pp directs agents; Endless tracks projects. Endless could show h2 pod status as a data source. |
| **Scion** | Orthogonal. Same relationship as h2 — agent infrastructure, not project awareness. |
| **Vigil** | Subsumed. Vigil's poll→diff→debounce→commit pattern becomes the private companion sync engine within Endless. |
| **Yggdrasil** | Future integration. If Obsidian becomes a desired interface, Yggdrasil bridges Endless-managed markdown into a vault. |
| **go-tealeaves** | Dependency. When Endless gets a TUI in Phase 2, it uses go-tealeaves components. |
| **go-cliutil** | Dependency. When Endless is ported to Go in Phase 2, it uses go-cliutil for CLI parsing. |
| **go-gitutils** | Dependency. Used for the private companion auto-commit system. |
| **Git commit TUI** | Complementary. The TUI handles intentional commits to project repos; Endless handles shadow version history for its own tracking purposes. |
| **Homelab tool** | Potential integration. A self-hosted dashboard/Notion-equivalent could eventually host Endless's web interface. |
| **SaveTabs** | Prior art. Browser-to-local-server communication pattern directly reusable for the Endless browser extension. |

---

## Resolved Decisions (from design review)

1. **Web dashboard technology: deferred.** Astro's strength is documentation sites via Starlight, which may not be the right fit for a dashboard. The choice between Astro, Go+Templ+HTMX, or another option will be made when Phase 1b begins, informed by whatever the Phase 1a CLI experience reveals about data access patterns.

2. **Browser extension communicates via API POST to Endless's local server.** No native messaging host needed. The SaveTabs project already implements this browser-to-local-server pattern and serves as direct prior art. The same HTTP server that powers the web dashboard receives extension POSTs.

3. **Daemon management: do it right, but not yet.** In the long term, launchd plist is the correct macOS answer. In the short term, any working approach is fine. This is not worth blocking on.

4. **`.endless/` is committed; `.endless/private` is not.** The `.endless/config.json` file is committed to the project repo so that the project's Endless configuration is visible, discussable, and readable by Claude Code. The `.endless/private` manifest (which lists files managed in the private companion) is excluded via `.git/info/exclude` per the zero-leakage principle: even the *existence* of private file management must be invisible to observers of the public repo.

5. **Document dependency tracking at file-level AND region-level.** File-level is the MVP baseline. Region-level tracking adds finer granularity where Claude defines what a "region" means — a heading section, a fenced code block, a frontmatter field, or any other semantically meaningful boundary. Claude proposes region definitions and dependency mappings; the developer confirms or rejects them. Emergent behaviors are expected and welcomed — the system will settle toward an optimal approach over time.