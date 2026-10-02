# Split storage: one machine database plus one database per project

## Shape (decided)

- Machine database stays at `~/.config/endless/endless.db`: sessions, panes,
  processes, worktree locks, jobs, errors — machine-scoped state.
- Each project gets `~/.config/endless/projects/<slug>.db`, outside the repo
  (discourages direct edits, one file per project regardless of worktrees,
  keeps the single ConfigDir() seam). Rebuilding a project deletes and
  rebuilds that file from that project's ledger, and nothing else.
- Cross-file foreign keys: the machine database keeps a lightweight MIRROR
  TABLE per kind it must reference (e.g. `main.tasks` with id, project_id and
  synced fields like title/description) — not one generic registry. Machine
  tables FK to the mirrors, so SQLite enforces them. Mirrors are synced by an
  idempotent reconcile job and refreshed for a project when that project is
  rebuilt; they never rely on a cross-file transaction (not atomic in WAL).
- Cross-project reads use ATTACH. Default limit is 10 attached files; a
  later child adds a per-project opt-out for users above that.
- "Main" means the machine database plus every project database under
  ConfigDir(); the self-dev sandbox mirrors that layout under its own config
  dir.

## Children to plan

1. Table inventory: classify every table in schema.sql as machine or project,
   and list the mirror tables (and synced fields) the machine side needs.
   Anything project-owned found missing from the ledger goes to the
   "emit events for mutations the projection must rebuild" work, not here.
2. Mirror tables + reconcile job.
3. Per-project database resolution through the `--db main|sandbox` context
   and the sandbox.
4. Cross-project reads via ATTACH: project list/status, session
   status/monitor, task next across projects, web dashboard, job runner.
5. Schema migrations reaching N files (with the goose work).
6. One-time migration of today's single database into the split.
7. (later) Per-project ATTACH opt-out.

## Out of scope

Task ID format and distributed ID allocation stay with the event-sourcing
epic. Today's machine-wide ordinal sequence keeps mirror keys unique on one
machine until then.
