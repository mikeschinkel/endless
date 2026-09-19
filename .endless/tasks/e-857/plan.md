# Plan: Session Conversation History (E-857)

## Context

During long sessions, important discussion between Mike and Claude gets buried under tool output and lost to compaction. Mike needs to review conversation history — especially recent messages — without scrolling through hundreds of tool calls. This feature captures conversation content via hooks and displays it via CLI and web dashboard.

No existing tool provides this integrated with a task tracker. Beads handles structured data sync, not conversation transcripts.

### Prior Art Review

66 tools surveyed (see `docs/private/claude-chat-display-shortlist-tools.md`). Mike tested 7 hands-on.

**Hands-on evaluation results:**
- **AgentsView** (Wes McKinney) — best overall UX. Clean list+detail layout, good information density. Reference for web UI.
- **carn** — best TUI UX. Two-pane list-on-left, details-on-right. Better than drill-down approaches.
- **claude-history** (raine) — best noise-filtering. Toggle tool calls off/truncated/full with one keystroke.
- **ccrider** — decent, has CLI search. Not as polished as carn.
- **Mantra** — overwhelming, opposite of what we want. Too much at once.
- **claude-session-browser** — too limited. Only finds `claude --resume` command, not enough info to pick a session.
- **claude-session-manager-tui** — unusably bad colors. Installed at `~/Projects/go-3rd-party/claude-session-manager-tui`.

**Key insights from evaluation:**
1. **First prompt is useless for identification** — all tools show the first user message as the session summary, but it rarely tells you what the session is about. Claude's recap after compaction would be much better. We should store/generate a session summary.
2. **Ability to hide/ignore sessions** — spawned test sessions and duplicates clutter the list. Need a way to mark sessions as hidden.
3. **Two-pane layout** (carn-style) beats drill-down for TUI/web
4. **Noise-filtering** (claude-history) is essential, not a nice-to-have

**Patterns to adopt:**
- **JSONL-is-source, DB-is-index** (carn) — JSONL files are authoritative, DB is derived and rebuildable via reimport
- **Three-level tool visibility** (claude-history) — off / truncated / full
- **FTS5 for cross-session search** (ccrider) — enables "what did we decide about tiers?" across all sessions
- **PreCompact hook** — Claude Code fires this before compaction; capture transcript at that moment too
- **Session summary** — store Claude's first substantive assistant response (or recap after compaction) as the session description, not the first user prompt

**Important constraint:**
- Claude Code deletes JSONL files after 30 days (configurable via `cleanupPeriodDays`). DB storage is essential, not just convenient. Reimport of existing files is time-sensitive.

**Noted for later (not this iteration):**
- Mantra's causality mapping (which reads led to which writes)
- claude-replay's publishable/curated session artifacts
- cclens's analytics (cost, context window hotspots)
- ccrider's MCP server exposing search to Claude

## Approach: Hook-Driven DB Capture + CLI/Web Display

### Data Capture

**How**: On `UserPromptSubmit`, `Stop`, `PreCompact`, and `SessionEnd` hook events, read the JSONL transcript file incrementally (from last-read offset), parse new messages, and insert into `session_messages` table. Track byte offset in `sessions.transcript_offset`.

**Why incremental**: The hook is short-lived (fires per event). Reading only new bytes keeps it fast. The JSONL file survives compaction, so we always have full history on disk — but only for 30 days.

**What to store** (type and role are different fields — type is the JSONL line type, role is inside the message object):
- `type=user` AND `role=user` with actual text content → user prompt (skip tool_result content, meta, system)
- `type=assistant` AND `role=assistant` → concatenated text blocks (skip thinking blocks)
- `type=assistant` tool_use content blocks → tool name + truncated input (500 chars)
- Skip: file-history-snapshot, progress, permission-mode, attachment, queue-operation, system

**session_messages vs activity**: `activity` records coarse events ("a tool was used at 3pm"). `session_messages` stores conversation content ("what was said at 3pm"). Keeping both for now; evaluate consolidating once session_messages is proven.

### Schema

```sql
CREATE TABLE session_messages (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'tool_use')),
    content TEXT NOT NULL,
    tool_name TEXT,
    message_uuid TEXT UNIQUE,
    created_at TEXT NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
);
CREATE INDEX idx_session_messages_session ON session_messages(session_id, created_at DESC);

-- Full-text search across all session messages
CREATE VIRTUAL TABLE session_messages_fts USING fts5(
    content,
    content=session_messages,
    content_rowid=id
);

-- Triggers to keep FTS in sync
CREATE TRIGGER session_messages_ai AFTER INSERT ON session_messages BEGIN
    INSERT INTO session_messages_fts(rowid, content) VALUES (new.id, new.content);
END;
CREATE TRIGGER session_messages_ad AFTER DELETE ON session_messages BEGIN
    INSERT INTO session_messages_fts(session_messages_fts, rowid, content) VALUES('delete', old.id, old.content);
END;
```

Add to sessions:
- `transcript_offset INTEGER NOT NULL DEFAULT 0`
- `transcript_path TEXT` — JSONL file path, captured from hook payload on SessionStart
- `summary TEXT` — session description derived from Claude's first substantive response or recap, not the user's first prompt
- `hidden INTEGER NOT NULL DEFAULT 0` — hide from session list (for test/spawn duplicates)

### Files to Modify/Create

1. **`sql/schema.sql`** — add session_messages table + FTS5
2. **`internal/monitor/db.go`** — migration for new table + columns
3. **`src/endless/db.py`** — same migration for Python
4. **`internal/monitor/transcript.go`** *(new)* — JSONL parser, incremental reader, DB inserter
5. **`cmd/endless-hook/claude.go`** — call ParseTranscript on UserPromptSubmit, Stop, PreCompact, SessionEnd; capture transcript_path on SessionStart
6. **`src/endless/cli.py`** — add `session` group with `history`, `list`, `search`, `reimport` commands
7. **`src/endless/session_cmd.py`** *(new)* — CLI implementation
8. **`internal/web/server.go`** — add `/sessions/{id}` route
9. **`internal/web/handlers.go`** — session history handler
10. **`internal/web/queries.go`** — GetSessionMessages, GetSessionInfo, SearchSessionMessages
11. **`internal/web/data/types.go`** — SessionMessageView, SessionView
12. **`internal/web/pages/session_history.templ`** *(new)* — conversation display template

### Session ID Resolution

Session IDs accept any of:
- `42` — integer DB id (shortest, most convenient)
- `009520ba` — short UUID prefix (first 8+ chars, like git short hashes)
- `009520ba-6ee9-4672-bac0-11f617eb1f77` — full UUID

The resolver tries integer first, then prefix match on `session_id`. Claude Code generates the UUIDs so we can't change that, but our integer ID is simpler for human use. With 8 hex chars (32 bits), collision probability across ~100 sessions is negligible. If a prefix is ambiguous, error and ask for more chars.

### Backfill: `endless session reimport [session_id]`

Re-reads the JSONL transcript from offset 0, inserting any messages not already in the DB (deduplication via `message_uuid`). Use cases:
- Backfill history from before this feature existed
- Self-healing when hooks had bugs and missed messages
- `endless session reimport` (no id) — reimport all sessions that have a known transcript path
- `endless session reimport <id>` — reimport a specific session
- **Time-sensitive**: JSONL files are deleted after 30 days. Run reimport on existing files promptly.

The `sessions` table needs a `transcript_path` column to store the JSONL file path (captured from the hook payload's `TranscriptPath` field on SessionStart). For reimport-all, also scan `~/.claude/projects/` for JSONL files and match to sessions by UUID in filename.

### CLI: `endless session history <session_id>`

```
endless session history <id>              # user + assistant messages, newest first
endless session history <id> --tools      # include tool calls (truncated: name + first few lines)
endless session history <id> --tools full # include full tool call content
endless session history <id> --timestamps # show timestamps on each message
endless session history <id> --limit 20   # default: 20
endless session history <id> --sort asc   # chronological (oldest first)
endless session list                      # recent sessions with project/state/summary
endless session list --all                # include hidden sessions
endless session hide <id> [id...]         # hide sessions from list
endless session unhide <id> [id...]       # unhide sessions
endless session search "query"            # FTS5 search across all sessions
endless session reimport [id]             # backfill from JSONL files
```

Default output (clean, labels in gray):
```
User: Is it possible to have two open terminals?

Claude: Yes. The issue is that both terminals are attached to the same session...
```

With `--timestamps`:
```
USER [2026-04-24 12:34:05]
Is it possible to have two open terminals?

CLAUDE [2026-04-24 12:34:12]
Yes. The issue is that both terminals are attached to the same session...
```

With `--tools` (truncated by default):
```
User: Is it possible to have two open terminals?

Claude: Yes. The issue is that both terminals are attached to the same session...

  Tool: Bash: tmux ls
    (3 lines of output)

Claude: Here's what I found...
```

Default sort: reverse chronological (newest first). `--sort asc` for oldest first. Default limit: 20 messages.

### Web: `/sessions/<session_id>`

- Session list page at `/sessions` showing all sessions with summary, project, message count
- Header: session metadata (project, state, active task, time range, summary)
- Messages: reverse chronological (newest at top for quick scanning)
- User messages: styled distinctly from assistant
- Tool calls: collapsed `<details>` elements within assistant turns (three states: hidden/truncated/full via click)
- Lazy loading: HTMX `hx-trigger="revealed"` loads older messages at bottom
- Route: `GET /sessions/{id}` for page, `GET /sessions/{id}/messages?before={id}` for lazy load partial
- Provide "before" links for each message so clicking would reload starting with that message
- `GET /sessions/{id}/messages?sort=asc` would display chronological (oldest at top)

### Session Summary Generation

The `summary` field stores a meaningful description of what the session is about. Derived automatically:
- On first assistant text response: extract the first 1-2 sentences as initial summary
- On PreCompact: if Claude produces a recap/summary, use that instead (it's better than the first response)
- `endless session summarize <id>` — manually regenerate summary from messages (future: could use Claude API)

For `session list`, show the summary instead of the first user prompt. This is the single biggest UX improvement over every tool Mike evaluated.

### Implementation Sequence

1. Schema + migrations (both Go and Python) — session_messages + FTS5 + session columns (transcript_offset, transcript_path, summary, hidden)
2. Transcript parser (`internal/monitor/transcript.go`) — including summary extraction
3. Hook integration (call parser on UserPromptSubmit, Stop, PreCompact, SessionEnd; capture transcript_path on SessionStart)
4. Build + verify data is being captured
5. CLI commands (`session history`, `session list`, `session hide/unhide`, `session search`, `session reimport`)
6. Run reimport on existing JSONL files (before 30-day cleanup)
7. Web data layer (queries + types)
8. Web handler + template (session list page + session detail page)

### Verification

1. Run `endless session reimport` to backfill from existing JSONL files
2. Run `endless session list` — should show sessions with message counts
3. Run `endless session history <id>` — should show user/assistant messages
4. Run with `--tools` — should also show truncated tool calls
5. Run `endless session search "tier"` — should find messages across sessions
6. Start a new session, have a conversation — hooks should capture automatically
7. Visit `http://localhost:8484/sessions/<id>` — should see formatted conversation
8. Scroll to bottom — lazy loading should fetch older messages
