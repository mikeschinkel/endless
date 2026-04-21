# Talk: Juggle 10+ Projects in Parallel Without Getting Overwhelmed

**Event:** AI Tinkerers Atlanta: Community Demos & Technical Deep Dives
**Date:** April 21, 2026, 4:30–7:30 PM EDT
**Format:** 5-minute live demo, no slides
**Speaker:** Mike Schinkel

---

## Three Questions

Their guidance: *"Rehearse your demo in three beats: what you built, what technologies you used, and what another builder will learn from your approach."*

1. **What I built:** A local dashboard and CLI that keeps me — the human — oriented across 10+ parallel AI coding projects so I can capture ideas, stay on task, and never lose my place.

2. **What technologies I used:**
   - Claude Code Subscription — _not_ API keys 
   - Go for Claude Code hooks 
   - Python for rapid CLI prototyping
   - Go for CLI distribution
   - Shell scripting for things only shell scripting can do
   - ZSH & Bash CLI prompt hooks
   - SQLite with a explicitly simple schema
   - Go + templ/templUI + TailwindCSS + AlpineJS + HTMX for web dashboard
   - Terminal tabs, or tmux if you want to supercharge them
   - Claude Channels for Claude session-to-session communication
   - Even with 40 years coding experience, I did almost no coding; just planning

3. **What another builder will learn:**
   - Why, where and how I used those technologies.

Compared to the Go code I write when I am not vibe-coding, this code is crap. But I have found that trying to make it quality just slows me down so much, and I will add in steps later to have AI omprove the code for me. (I told it to use sqlc with Go; it didn't; it just hacks the SQL.)

---

## The Real Story

I'm a solo developer running 10+ parallel projects with Claude Code in separate terminal tabs. AI coding assistants have made genuine multi-project parallelism possible for the first time — but **I** can't keep track of it all. The AI remembers fine; I'm the bottleneck.

Without Endless, I would:
- Go down rabbit holes chasing every idea so I wouldn't forget it
- End up with every project/tab in a different state, unable to remember where I was
- See a proliferation of markdown files from constantly revising planning documents
- Lose track of what I decided, what the next step was, what conversation happened where

With Endless, I can say **"record that and let's keep working on our main task"** or **"record that and let's work on it now."** Either way, both me and the AI stay on task. I can plan while implementing and implement while planning. I can step into a rabbit hole to discuss a feature idea and have Endless capture it — no fear of forgetting, no compulsion to do it right now.

The value is already huge, and we're barely using half the features yet.

---

## Talk Strategy

### Audience Expectations (from AI Tinkerers guidelines)

- "These are not pitches. Skip the market overview and jump right to the implementation."
- "Show, don't tell" — run it live or walk through the workflow/stack
- "Expose the internals" — code, configs, hooks, schema
- "The value is in *how* you built it, not just the end result"
- "Focus on a particular technical challenge" — don't show the whole project
- "Embrace imperfection" — half-baked is welcomed and respected

### What Makes This Talk Stand Out

**Situational awareness for the human, not the AI.** Tools like Beads help the AI remember. Endless helps *me* not forget — which projects need attention, what I was working on, what ideas I captured but haven't acted on yet. It turns my chaotic parallel workflow into something I can actually sustain.

### What to Emphasize

- **The human problem** — I can't keep 10+ projects in my head; the AI can, but I can't
- **"Record that and keep working"** — the workflow where ideas get captured without derailing focus
- **The monitoring pattern** — hooks observe content and actions, enabling the system to take appropriate action
- **The tech decisions** — Python for prototyping, Go hooks for performance, simple SQLite schema (pulled back from complexity), CLI the agent picks up easily, tmux as enhancement not requirement, session-to-session messaging
- **Most of this is planning, not coding** — I drove the decisions; AI generated the plans but I shaped the direction
- **Local-first stack** (Go + Templ + HTMX + SQLite) — fits "Homebrew Computer Club" ethos
- **Half-baked and shipping** — they love that

### What to Cut (Do NOT Include)

- Background or career story
- Why you started this project (the emotional journey)
- Any explanation of what Claude Code is (they know)
- Any slide deck
- Anything about the market or other tools
- Private companions / zero-leakage (cool but not the focus)
- Document lifecycle / staleness detection (secondary feature)
- Browser extension plans (vaporware)

---

## Script

### 0:00–0:30 — The Setup (spoken, no screen yet)

"I'm Mike, I write Go. I run 10+ parallel projects with Claude Code in separate terminal tabs. The problem isn't that AI is too slow — it's that *I* can't remember what I was doing yesterday across all of them. The AI remembers fine — I'm the bottleneck. So I built Endless — a situational awareness system for the human. Many projects, all at once."

### 0:30–1:30 — The Dashboard (live, browser at localhost:8484)

- Show the portfolio view with real projects, real statuses
- Click into one project — show the plan tree (hierarchical, with statuses)
- Show the action queue / notes — "here's what needs my attention right now"
- Say: "Go, Templ, HTMX, SQLite. Local-first. No cloud. The CLI is Python — I prototyped fast, the hooks are Go for speed, and the plan is to port everything to Go for single-binary distribution."

### 1:30–3:30 — Three Monitoring Examples: Pop the Hood (terminal, large font)

The hooks are the enabling pattern. They monitor content and actions, and the system takes appropriate action based on what it observes. Three examples:

**Example 1: Session context injection — Claude starts warm, not cold**

When Claude Code starts a session, Endless injects the current plan context. Claude doesn't start cold — it knows what project this is, what plan items are active, and what we were working on.

Show:
- Hook config in `~/.claude/settings.json` — the `SessionStart` entry calling `/usr/local/bin/endless-hook claude`
- The Go handler: `cmd/endless-hook/claude.go` lines 78–83 (SessionStart case) and lines 115–140 (`handlePlanContextInjection` — looks up active plan items, formats context, returns it as `additionalContext` JSON on stdout for Claude to read)
- Key point: "Claude Code's hook system lets you return JSON on stdout. The `additionalContext` field gets injected into Claude's context. So every new session automatically knows what we're working on."

**Example 2: Session-to-session messaging — two Claude sessions talk to each other**

When I'm working on Project A and realize I need to tell the Claude session in Project B something, I don't have to switch tabs and interrupt it. Endless provides a beacon/connect protocol — one session broadcasts, another connects, and they exchange messages through SQLite.

Show:
- The messaging code: `internal/monitor/messaging.go` — `CreateBeacon()` (line 34), `ConnectToChannel()` (line 88), `SendMessage()` (line 125), `GetPendingMessages()` (line 168)
- The schema: `msg_channels` and `msg_queue` tables in `sql/schema.sql` (lines 230–257)
- The CLI commands the agent uses: `endless channel beacon`, `endless channel connect <id>`, `endless channel send`
- Key point: "Two AI sessions in different tabs coordinating through a shared SQLite database. No API calls, no network — just rows in a table."

**Example 3: File change detection — Endless knows what changed without being told**

The ZSH prompt hook fires every time the terminal prompt renders. It records which project I'm in, detects file changes via hash comparison, and logs everything — throttled to avoid spam. Meanwhile, the Claude PostToolUse hook catches every file write Claude makes.

Show:
- The prompt hook: `cmd/endless-hook/prompt.go` (all 69 lines — it's short)
- The file detection: `internal/monitor/filewatch.go` — `DetectFileChanges()` with hash-based tracking
- The activity + file_changes tables in `sql/schema.sql` (lines 204–227)
- Key point: "Small database writes from shell hooks accumulate awareness. Instead of expensive full-context reviews where I re-read 20 files trying to remember what happened, Endless knows incrementally."

### 3:30–4:30 — The Schema: Simplicity Was Hard-Won

Briefly show `sql/schema.sql` — the full thing is ~260 lines, 17 tables.

"We started with a more complex schema and pulled back. The insight: you don't need a complex data model when your hooks are doing continuous small writes. The `activity` table, `file_changes` table, `plans` table, and `msg_queue` table do most of the work. Everything else is supporting cast."

Mention: "Most of this project has been planning, not coding. I drove the design decisions — the AI generated implementation plans, but I shaped what got built and what got cut. That's been the most valuable part of using AI for a project like this."

### 4:30–5:00 — The Close

"This is half-baked. I'm building it in public. But it's already changed how I work — I can juggle 10 projects because I can say 'capture that idea' and stay focused. The pattern is simple: hooks that monitor, SQLite that accumulates, a dashboard that surfaces what matters. You could build something like this for your own workflow."

---

## Code Reference (for rehearsal — know where everything is)

| What | File | Key Lines |
|------|------|-----------|
| Hook config | `~/.claude/settings.json` | Full file — shows all 6 hook events |
| Claude hook dispatcher | `cmd/endless-hook/claude.go` | L40–113 (main switch), L78–83 (SessionStart) |
| Plan context injection | `cmd/endless-hook/claude.go` | L115–140 (`handlePlanContextInjection`) |
| PreToolUse enforcement | `cmd/endless-hook/claude.go` | L203–266 (`handlePreToolUse`, `blockToolUse`) |
| PostToolUse file tracking | `cmd/endless-hook/claude.go` | L142–186 (`handlePostToolUse`) |
| Session command detection | `cmd/endless-hook/claude.go` | L276–317 (`handlePostToolUseSession`) |
| ZSH prompt hook | `cmd/endless-hook/prompt.go` | All 69 lines |
| File change detection | `internal/monitor/filewatch.go` | `DetectFileChanges()` |
| Inter-session messaging | `internal/monitor/messaging.go` | L34 `CreateBeacon`, L88 `ConnectToChannel`, L125 `SendMessage`, L168 `GetPendingMessages` |
| Activity recording | `internal/monitor/activity.go` | `RecordActivity()`, `ShouldThrottle()` |
| Session management | `internal/monitor/session.go` | `StartWorkSession`, `InitSession` |
| Full SQLite schema | `sql/schema.sql` | 269 lines, 17 tables |
| Python CLI entry | `src/endless/cli.py` | L29+ (Click group) |
| Web server | `cmd/endless-serve/main.go` | HTTP on port 8484 |
| Web routes | `internal/web/server.go` | /, /projects, /plans, /activity |
| Go hook entry point | `cmd/endless-hook/main.go` | Dispatcher: prompt, claude, codex |

---

## MacBook Pro Setup Checklist

The demo is developed on Mac Mini. Everything must work on the MacBook Pro by April 21.

### Step 1: Clone the repo

```bash
git clone <repo-url> ~/Projects/endless
cd ~/Projects/endless
```

### Step 2: Build Go binaries

```bash
just build
# Creates bin/endless-hook and bin/endless-serve
```

### Step 3: Install symlinks

```bash
just install
# Symlinks bin/endless-hook -> /usr/local/bin/endless-hook
# Symlinks bin/endless-serve -> /usr/local/bin/endless-serve
```

### Step 4: Install Python CLI

```bash
uv tool install --editable ~/Projects/endless
# Makes `endless` command available
```

### Step 5: Copy the database and config

```bash
mkdir -p ~/.config/endless
scp macmini:~/.config/endless/endless.db ~/.config/endless/
scp macmini:~/.config/endless/config.json ~/.config/endless/
```

The `config.json` contains scan roots (`~/Projects`) and ownership filters. Verify paths are correct for MacBook.

### Step 6: Copy Claude Code settings (hooks)

```bash
# MERGE — don't overwrite, the MacBook may have its own settings
# The critical section is the "hooks" block in:
~/.claude/settings.json
```

Key hooks to verify are present:
- `SessionStart` -> `/usr/local/bin/endless-hook claude` (async: false)
- `PostToolUse` -> `/usr/local/bin/endless-hook claude` (async: false)
- `UserPromptSubmit` -> `/usr/local/bin/endless-hook claude` (async: false)
- `Stop` -> `/usr/local/bin/endless-hook claude` (async: true)
- `SessionEnd` -> `/usr/local/bin/endless-hook claude` (async: true)

### Step 7: Copy Claude project memory

```bash
mkdir -p ~/.claude/projects/-Users-mikeschinkel-Projects-endless/memory
scp -r macmini:~/.claude/projects/-Users-mikeschinkel-Projects-endless/memory/ \
    ~/.claude/projects/-Users-mikeschinkel-Projects-endless/memory/
```

Note: The path encoding uses the Mac Mini's absolute path. If the MacBook uses different paths (e.g., different username), this may need adjustment.

### Step 8: Copy other registered projects (for dashboard to show real data)

The database references projects by path. For the dashboard to show real projects:
- Either clone the same projects to the same paths on the MacBook
- Or accept that some project paths won't resolve (dashboard should still show DB data)

### Step 9: Verify

```bash
# Test CLI
endless list

# Test hooks binary
echo '{"session_id":"test","cwd":"/tmp","hook_event_name":"SessionStart"}' | endless-hook claude

# Test web dashboard
endless serve
# Open http://localhost:8484

# Test ZSH prompt hook (if configured in .zshrc)
# Check: does endless-hook prompt fire on prompt render?
```

### Step 10: Pre-demo prep

```bash
# Start the web server
endless serve &

# Open terminal with large font (24pt+)
# Open browser to http://localhost:8484 with 150%+ zoom
# Have specific files ready to show in editor/terminal
```

### Things that WON'T come from git clone

| What | Location | How to transfer |
|------|----------|-----------------|
| SQLite database | `~/.config/endless/endless.db` | scp (includes all project data, plans, sessions) |
| Global config | `~/.config/endless/config.json` | scp (scan roots, ignore patterns) |
| Claude hooks config | `~/.claude/settings.json` | Manual merge of hooks block |
| Claude project memory | `~/.claude/projects/-Users-mikeschinkel-Projects-endless/memory/` | scp -r |
| Python CLI installation | via uv | `uv tool install --editable` |
| Go binary symlinks | `/usr/local/bin/endless-*` | `just install` |
| ZSH prompt hook | `~/.zshrc` or sourced file | Check if prompt hook line exists |
| Homebrew dependencies | varies | `brew install uv`, `go` (if not present), `tmux` |
| Registered project dirs | `~/Projects/*` | Clone needed repos or accept missing paths |
| Claude Code itself | installed globally | Verify `claude` command works |
| Tailwind CSS binary | used by `just build` | Should be handled by build process |
| templ binary | used by `just build` | `go install github.com/a-h/templ/cmd/templ@latest` |
