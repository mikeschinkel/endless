# E-2038 — refuse a binary that cannot read the DB it opens

## Problem

The `e-2011-home-relative-project-paths` change file ran against the shared main
DB at 2026-08-20T18:17:55, rewriting `projects.path` from absolute to `~/...`.
In self-dev every worktree carries its own `bin/endless-go`, built at its branch
point, so the migration instantly created a fleet of readers that cannot read
the new format. They compare a raw absolute cwd against a `~/...` column, miss
every rung of `ProjectIDForPath`'s walk-up, and fall through to
`ensureAutoRegisteredProject`, which silently registers the worktree as a
project named after its directory.

Evidence: zero stray project rows before that timestamp, five after. All five
store an ABSOLUTE path while every legitimate row is home-relative — proof the
writer predates E-2011. `e-1914` and `e-1733` run Aug-13 binaries with no
`homeRelative` symbol at all. At the time of measurement 114 of 156 worktrees
held a binary older than the migration.

E-2011 is not the bug. It is the first format change to expose a standing
hazard: nothing checks that a binary can read the database it opens, so every
future migration repeats this.

## Approach

`_schema_version` already names every applied change file, and a binary ships a
known set under `internal/schema/changes/`. That is the handshake.

1. **Compare at open.** In `monitor.DB()`, after schema application, read
   `_schema_version` and compare against the embedded change-file set. Markers
   in the DB that the binary does not ship mean the DB is AHEAD of the binary.

2. **Refuse loudly, and name both sides.** Exit non-zero with the unknown
   marker(s) and the remedy (`just build` in this worktree, or run main's
   binary). Silent misreading is the failure being fixed; a quiet degrade
   repeats it.

3. **Ahead-only, never behind.** A binary shipping changes the DB has not
   applied is normal — that is every pre-land worktree, and `apply-change` is
   how it resolves. Only DB-ahead-of-binary is refused.

4. **Do not gate the escape hatches.** `db apply-change`, `db path`, `db backup`
   and `rebuild-db` must still run, or a refused binary cannot be recovered
   with the tools that exist to recover it.

## Interaction with E-1704

E-1704 makes one binary self-re-exec into the worktree build. That does not
remove this: re-exec still lands in the stale build. The version check belongs
IN the routing decision — "re-exec only into a build that can read this DB,
else stay in main's" — which turns a hard refusal into a graceful fallback.
Sequence this after E-1704 if that lands first; the check is the same code.

## Cleanup (separate from the guard)

Five stray rows exist. Removing them is `project unregister <name>`, ONE name
per invocation. Note `tasks`, `decisions`, `notes`, `activity` and
`project_next` are `ON DELETE CASCADE` from `projects`; measured at filing time
these rows held 0 tasks, 0 decisions, 0 sessions and 153 activity rows. Cleanup
without the guard is temporary — any stale worktree re-registers on its next
hook event.

## Verification

- A DB carrying a `_schema_version` marker absent from the binary's change set
  is refused, and the message names the marker.
- A binary with changes the DB lacks runs normally.
- `db apply-change` / `db path` / `db backup` / `rebuild-db` run against an
  ahead DB.
- With the guard in place, a deliberately stale binary run from a worktree
  registers no project row.
