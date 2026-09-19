# Durable run-once git-local marker for non-detectable post-land one-shots

Speculative — capture the reasoning now; build only if a real need actually appears.

## Scope (decoupled)
This task is the durable *marker* — the "has this action already run in this environment?"
record — plus the `endless worktree git-path` command it needs to resolve its storage. The
*action* half (a task shipping a post-land script) is a separate `next` task and is now
**tracked** (`.endless/hooks/post-land/e-NNN.sh`), so it needs no git-local resolution. A
marker is needed **only** for actions whose completion **cannot be read off the world** — a
data backfill, a destructive-once op, external-state migration. Detectable cleanups (and
post-land *scripts* for them) need no marker.

## Why the DB is ruled OUT
The DB is a replay of the append-only JSONL ledger (ledger = WAL). Either failure applies:
- **Marker emitted to the ledger** → a pull replays "done" on *every* machine, including
  checkouts where the local action never ran → **false-done everywhere**.
- **DB-only column, not emitted** → an un-WAL'd mutation; any DB rebuild/replay wipes it →
  the action re-runs (or damages, if not idempotent).
So the marker must be what the DB is not: **local, non-replayable, surviving a DB rebuild**
→ git-local metadata (never committed, synced, or landed).

## Storage resolution — the shared-dir trap (proven)
Addressing a git-local path via `git rev-parse --git-path info/…` redirects to the
**common** `.git/info/` — shared across all worktrees of a clone (demonstrated: two
worktrees resolve `info/endless/x` to one physical file; a write via one is read by the
other). Per-worktree storage must instead resolve `git rev-parse --git-dir` (`.git/
worktrees/<name>/`) and append `info/endless/`.

## `endless worktree git-path` command
- Read-only subcommand printing the absolute **per-worktree** endless metadata dir
  `…/.git/worktrees/<name>/info/endless` (creating it if absent). Callers append a marker
  filename. Implemented via `--git-dir` + `/info/endless`, **never** `--git-path info/…`,
  so it can't hit the shared-dir trap. One source of truth for git-local per-worktree
  state. (Relocated here from the post-land-script task, whose script is now tracked and
  needs no git-local resolution — this command's only consumer is the marker.)

## Marker scope — a real fork (still open)
`git-path` gives per-worktree; the common `.git/info/` gives shared-per-clone. Which is
correct depends on what the action reconciles (clone-wide state → shared; per-tree state →
per-worktree). Proposal: let each action **declare its scope**; always resolve via the
command above, never a hardcoded path.

## Representation — open
A git-local `…/info/endless/` **directory** (the endless namespace); the per-marker
representation inside (sentinel filename, key in a JSON file, other) is open.

## Not motivated by E-1755
E-1755's need was untracked-cruft cleanup (git-native, detectable) — handled by the
post-land script + land-guard follow-ups, NOT by a marker. Reason this task from genuine
future non-detectable cases, not from E-1755.
