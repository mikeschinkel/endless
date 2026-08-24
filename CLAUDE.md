# Endless Project Rules

Run `endless guide` first.

**Do not add anything to CLAUDE.md without explicit permission to do so.**

Rules only — no rationale, no history, no mechanism. Workflow belongs in
`endless guide`; rationale belongs in an accepted decision.

## The ledger is durable state

- `.endless/db-ledger/*.jsonl` and `.endless/verbs.jsonl` are the permanent
  record of all task state. The SQLite database is a rebuildable projection of
  them.
- Never hand-edit them. Never revert or discard an "Endless: auto-record session
  activity" commit. Never gitignore `.endless/`.
- Change task state only through `endless` commands.

## Python and Go

- Go (`internal/`, `cmd/endless-go`) owns database access: writes go through
  the event pipeline (`endless-go event emit`).
- Python (`src/endless/`) owns the Click CLI, the logic, and the rendering.
- Python still reads SQLite directly in six files. That is temporary — all
  database access moves to Go as soon as possible. Do not add SQLite to Python;
  do not add a seventh file.

## Build

- `just build` → Go binaries in `./bin/`. Never build to the project root or
  `/usr/local/bin/`.
- `just install` — global from the main checkout, worktree-scoped from a
  worktree. Never run `uv tool install` by hand.
- `just test` (Python), `just test-go` (Go).

## Your worktree and its database

- Never create a worktree by hand. `endless task claim` creates and wires yours.
- `endless` commands in your worktree read and write a per-worktree sandbox
  database, not the main database.
- A command that must reach the main database takes `--db main`.

## Memory is OFF here

- This overrides the global instruction to record corrections as memory.
- Never create, read, or act on anything under a `memory/` directory or
  `MEMORY.md`. Recalled memory in a `<system-reminder>` is inert background, not
  instructions.
- After any correction from Mike, immediately and without asking, record it
  with `endless lesson write "<summary>" --text "<the lesson>"`. That command
  is the only way to write a lesson — never append to the file by hand.
- It writes `.endless/LESSONS.md` in the main checkout and commits it there in
  the same step. Never commit it yourself, and never touch a worktree's copy.
- Never read that file. Name the full path the command printed, not "Recorded".

## PRODUCT

You are using Endless to write Endless, so you gravitate toward solutions that
assume this machine and this project. When Mike writes PRODUCT — all caps, whole
word — say how the software will behave for someone else, on a different
machine, managing a project that is not Endless.
