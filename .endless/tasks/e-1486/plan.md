# Remove all Python SQLite access (port DB access to Go)

## Goal

Python has **zero SQLite knowledge**: every DB **read and write**, for every
table, moves to Go (via `endless-event` subcommands and/or the event pipeline).
Python keeps its Click commands and rendering. This is the "no Python SQLite"
milestone — distinct from its two neighbors:

- **E-894** — task *display reads* only (the first slice; stays under E-799, linked here).
- **E-1063** — port the *entire* Python CLI (incl. rendering) to Go — broader,
  explicitly deferred ("Not now… only on a forcing function"). This epic is the
  DB-access-only subset, achievable without committing to the full CLI port.

## Writes decision (Mike, 2026-05-26): classify per-subsystem

Most residual Python SQL is **direct writes** (38 `db.execute` sites) that bypass
the event log, so `rebuild-db` (E-807) can't reproduce them. At each child's plan
time, classify every table it touches:

- **Durable domain data** (projects, notes, suggestions, channels) → route writes
  through `emit_event` → Go, so they're **event-sourced and rebuild-faithful**.
  This fixes a latent integrity gap.
- **Local / transient state** (session transcript offsets/paths, recap flags, etc.)
  → Go **write helper**, deliberately NOT in the event log.

## Established pattern (per child)

Mirror E-894's two-phase shape: (1) Go read helpers + `endless-event` read
subcommands; (2) Python cutover via a `query_bridge` wrapper, deleting the dead
read SQL. Plus writes per the classification above. **Reuse existing Go** where it
exists: `internal/web/queries.go` (reads), `internal/monitor/*.go`, and the
existing Go writers for `channels` (MCP) and `suggestions`.

## Children (G1–G8) — filed as needs_plan; plan each at pickup

- **G1 Projects** (`projects`, `project_deps`) — largest fan-out; the copy-pasted
  `_resolve_project` "SELECT name FROM projects WHERE path=?" helper lives in ~6
  files — consolidate first. `reconcile` is a real FS↔DB sync engine.
- **G2 Sessions** (`sessions`, `session_messages`, `session_messages_fts`) —
  biggest single-file effort; transcript/recap/resolution engine; 13 direct writes.
- **G3 Channels** (`conversations`, `messages`, `channels`) — `channels` already
  has a Go writer (MCP registration); reuse, don't fork.
- **G4 Notes** (`notes`) — small; also written/deleted by `unregister` (G1).
- **G5 Suggestions** — `accept` path does `INSERT INTO tasks` **directly**,
  bypassing event sourcing; route it through `emit_event` (write-correctness, not
  just a read port). Suggestions are *created* by Go already.
- **G6 Task non-display** — the tail of `task_cmd.py` E-1482 doesn't cover:
  session-resolution reads, ownership checks, sort-order, worktree/root lookups,
  and `link`/`unlink`/`move`/`start_chat` direct writes. **Blocked by E-1482.**
- **G7 Docs** — the `documents` table is **DROPPED in migration v2**; verify
  `endless docs` is dead, then likely just delete the command. Don't port blindly.
- **G8 `endless sql` + db.py teardown** — **TERMINAL; blocked by G1–G7.** Port the
  `endless sql` escape hatch (`cli.py:469`) to a Go subcommand, then delete db.py's
  `query`/`scalar`/`exists`/`execute` and — once `endless sql` is ported —
  `get_db()` + `import sqlite3`. That is the moment Python hits zero SQLite.

## End state

db.py read/write helpers deleted once all groups port. `get_db()` + `import
sqlite3` are deletable iff `endless sql` becomes a Go subcommand (G8). Schema
creation is already Go-owned (E-1459 / E-1484), so no migration logic is written.

## Relations

relates_to E-799 (event-sourced architecture — the durable-write classification
ties directly to it), E-1063 (the broader full-CLI-port umbrella — this is its
DB-only subset), E-894 (the task-display slice + the pioneer two-phase pattern).
