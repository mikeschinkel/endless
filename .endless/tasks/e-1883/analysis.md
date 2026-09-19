# Context

Raised by Mike on 2026-08-04 during E-698 (fire-once job runner), while
discussing why `session monitor` inside a self-dev worktree is pinned to the
real `~/.config/endless/endless.db`.

## The flaw

- `db-ledger` is **project-scoped**: it lives at `<project>/.endless/db-ledger`,
  is committed to that project's repo, and is designed so the Endless DB can be
  rebuilt from it.
- `~/.config/endless/endless.db` is **machine-global**: one SQLite file holding
  every registered project's tasks, decisions, events, landings — plus
  machine-local state (sessions, panes, worktree locks).

So the rebuild guarantee does not actually hold. Rebuilding the DB from project
A's ledger reconstructs A's rows and **loses every other project's data**. The
guarantee is only true on a single-project machine.

## Options to weigh (nothing decided)

1. **Split the databases.** One machine-local DB (sessions, panes, machine
   state) + one DB per project (tasks, decisions, events, landings). A project's
   ledger then rebuilds exactly its own DB and nothing else. Cleanest mapping of
   ledger scope to DB scope; biggest change (every query, the `--db` context
   machinery, cross-project views like `session status`).
2. **Project-scoped rebuild.** Keep one DB; make rebuild surgical — replace only
   the rows belonging to the project being rebuilt, inside a transaction.
   Smallest change; leaves the scope mismatch in place and every rebuild is a
   delicate partial write.
3. **Machine-global ledger alongside the per-project ones.** A second ledger for
   machine-scoped rows so the machine DB is itself rebuildable. Complements 1 or
   2 rather than replacing them.
4. **ATTACH per project.** One file per project, attached on demand, so
   cross-project queries still work with `project.tasks` qualification.
5. **Do nothing; document the limitation.** Rebuild is declared single-project
   only, and cross-project machines are told to back up instead.

## Constraints to respect

- The self-dev sandbox (E-1281) and the `--db main|sandbox` context threading
  (E-1429) both assume a single DB path resolved from `ConfigDir()`. Any split
  has to keep that seam coherent.
- `session status` / `session monitor` deliberately read machine-global session
  state regardless of cwd (`PinMainDB`, E-1450/E-1685). Splitting must not
  break that.
- Cross-project reads exist today (`endless project list/status`, the web
  dashboard).

## Not yet decided

Mike explicitly wants to brainstorm options before anything is locked in. Do not
implement from this text.

## Evidence: a foreign project's task mutation landed in this project's ledger

Observed 2026-08-08 while bulk-routing `unplanned` tasks to `untriaged`. This is
the problem above, one step worse than stated: it is not only that rebuilding
one project's ledger LOSES other projects' data — a project's ledger can also
CONTAIN another project's task mutations.

`endless task update` resolves the event's `project` from CWD, not from the task
being updated. Running it from the endless checkout against E-1629 (a
`go-tealeaves` task) emitted an event stamped `"project":"endless"` into
`endless/.endless/db-ledger/`. go-tealeaves has no record that its own task
changed; endless' ledger claims a task that was never its own. Under E-1309's
routing (all ledger commits go to the project main checkout resolved from the
event's `project`), a wrong `project` also sends the git commit to the wrong
repository.

So a rebuild is wrong in both directions: replaying endless' ledger would
resurrect a go-tealeaves task row, and replaying go-tealeaves' ledger would miss
a change to its own task. Whatever scoping option is chosen, event-to-project
attribution has to be derived from the ENTITY, not from the caller's cwd, or the
same corruption reappears under a per-project ledger.

Reproduced by hand; the workaround is to run per-project from each project's
directory (which is how the rest of that bulk operation was completed).
