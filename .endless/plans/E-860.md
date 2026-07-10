## Goal

Background-generate per-session recap summaries from the last N user+assistant messages (no tool calls), refresh when sessions accumulate new user messages.

## Motivation

Claude Code's interactive `/recap` is UI-only — no CLI access, no sidecar file. Sessions list by first prompt which is useless. Capturing recaps into Endless's `sessions.summary` field makes session re-entry dramatically better.

## Approach

**Generation:**
- Query last 20 user+assistant messages from `session_messages` (no tool_use role)
- Pipe to `claude -p "<recap prompt>"` 
- Store result in `sessions.summary`, update `sessions.summary_seq` watermark

**Trigger: needs_recap flag**
- Hook sets `needs_recap = 1` on `Stop`/`SessionEnd`
- Also set when user message count since last recap exceeds 10

**Execution (no daemon needed):**
- **Shell prompt hook** (ZSH/Bash) — checks for `needs_recap` sessions, generates in background (`&`). Fires on every terminal prompt, asynchronous from Claude.
- **`session list`** fallback — generates for flagged sessions inline if prompt hook not installed
- **`endless session recap [id]`** — explicit manual generation

**Cost control:**
- Recap from last 20 user+assistant messages only (not full transcript)
- No tool calls in recap input — they're implementation noise
- Only recap when `needs_recap = 1` AND >= 10 new user messages since last recap
- No blanket background generation — only when someone is looking

## Schema additions to sessions table
- `needs_recap INTEGER NOT NULL DEFAULT 0`
- `summary_seq INTEGER NOT NULL DEFAULT 0` — user message count watermark at last recap

## Related
- E-857 (session conversation history — provides the message data)
- E-859 (live TUI viewer — separate concern)
- E-698 (daemon — future home for background tasks, not needed for this)
