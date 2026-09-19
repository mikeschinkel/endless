# What SHOULD we be preserving of a session, and where?

## The broken assumption

Mike expected **all sessions to be recorded to version control for posterity**.
They are not. Verified 2026-08-16:

- `.endless/db-ledger/*.jsonl` (9.1 MB, committed) carries ONLY task/decision
  events. Full kind census: `task.fields_updated` 2905, `task.created` 1814,
  `task.status_changed` 1397, `task.landed` 545, `task_dep.created` 286,
  `epic.status_derived` 142, `task.claimed` 125, `decision.*`, `focus.*`, and
  12 × `session_status.recorded`. **No event kind carries session message
  content.**
- `session_messages` (92,545 rows, 32 MB) lives ONLY in SQLite at
  `~/.config/endless/endless.db`. Not committed, not exported, not backed up
  beyond the rolling `backups/` copies.
- The 23 ledger lines that match the string `session_messages` are task
  descriptions that happen to mention the table name in prose.

So conversation history survives exactly as long as one SQLite file on one
machine. That is the gap to close, and this task is to decide HOW — not to
implement a particular answer.

## What is already recorded, and how lossy it is

`monitor.ParseTranscript` projects Claude's transcript into `session_messages`
with three roles:

| role | what is stored | fidelity |
|---|---|---|
| `user` | text | full; tool-result blocks explicitly SKIPPED |
| `assistant` | text blocks joined | full; `thinking`/`signature` blocks skipped |
| `tool_use` | `name + ": " + input` | **truncated to 500 chars** (`transcript.go:223`) |

Two deliberate losses worth re-examining:

1. **Tool input is truncated at 500 characters.** Nobody asked for this. It is
   the difference between being able to debug a prior session and not: an `Edit`
   or `Bash` input is routinely longer than 500 chars, and the tail is where the
   actual change lives.
2. **Tool RESULTS are not captured at all.** `ParseTranscript` skips content
   starting with `<` or `{"tool_use_id"`, which is how results arrive. So
   `session history --tools` shows what was asked for and never what came back.
   Same question as truncation: should results be recorded, and at what fidelity?

## Existing full-fidelity sources, currently unmined

Both are outside Endless and outside version control, but they exist today:

- `~/.claude/logs/hook.log` — **534 MB**. Pretty-printed JSON hook payloads
  (`PreToolUse` etc.) carrying the COMPLETE `tool_input`, untruncated.
- `~/.claude/projects/` — **218 MB**. Claude Code's own JSONL transcripts; the
  authoritative record, and the very thing `ParseTranscript` reads at hook time
  before discarding most of it.

Also present: `~/.claude/logs/session-log.jsonl` (316 KB) and
`tmux-alert-debug.log` (2.2 MB).

If the answer is "preserve more", the question is whether to capture more at hook
time going forward, backfill from these, or both.

## Duplication / compression

Boilerplate prompt text repeats verbatim across sessions. The archive-decision
skill prompt appears **149 times across 133 real sessions** (each a genuine user
turn inside a long conversation — not a throwaway session, so it cannot simply be
dropped at record time without making `session history` misrepresent what was
typed). Two retired `claude -p` generators left ~200 more rows before they were
removed (last: `Write a one-line summary…` 2026-05-29).

Compression is worth designing rather than assuming: some skills take arguments,
so identical-prefix dedup only goes so far and the general case needs something
smarter than exact matching.

## The mass, for scale

`tool_use` is **56,236 of 92,545 rows (61%) and 14 of 32 MB (44%)** — and that is
AFTER the 500-char truncation. Whatever the answer is, tool rows dominate it.
Mike's position going in: tool_use history is important and worth keeping; how to
minimize it is a separate, later question.

## Questions to settle

1. Should session history be version-controlled at all? If so: the existing
   `.endless/db-ledger`, a separate committed export, or something else?
2. If committed — what is the unit and cadence? Per session on end? Periodic
   export? What happens to a 32 MB (and growing) corpus in git?
3. Keep the 500-char truncation, raise it, or remove it?
4. Record tool results? At what fidelity?
5. Compression strategy for repeated boilerplate, including skills with args.
6. Backfill from `hook.log` / `~/.claude/projects`, or only capture forward?
7. Retention: does anything ever age out, or is posterity literal?

## Out of scope

- **E-1925** (`session recap`) — it reads whatever `session_messages` holds and
  is unaffected by this decision. Do not couple them.
- The **rune-safe truncation bugfix** — that fixes byte-slicing corruption in the
  truncation that exists today; it is correct regardless of whether this task
  later removes truncation entirely.
