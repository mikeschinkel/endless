# E-1883 — decide whether and how to scope Endless databases per project

A brainstorm. Its deliverable is the outcome: a recommendation Mike can accept,
with the evidence behind it. It changes no code.

## Questions to settle, in order

1. **Is a split needed at all?** Weigh both drivers in the context:
   - project-owned state: what belongs to a project stays in its repo;
   - rebuild safety: rebuilding from one project's ledger must not lose any
     other project's data.
   For each driver, show what the committed db-ledger already provides and
   what it does not. If the ledger plus option 2 (surgical project-scoped
   rebuild) or option 5 (document the limit) meets both, recommend that and
   stop.
2. **If a split is needed, which shape?** Compare options 1 (a machine DB plus
   one DB per project), 4 (ATTACH per project) and 3 (a machine ledger, as a
   complement), each against:
   - every cross-project read today: `project list/status`, `session
     status/monitor`, `task next` across projects, the web UI, the job runner
     (auto-spawn, unlanded cache, backup);
   - the `--db main|sandbox` context and the self-dev sandbox, which assume one
     path from `ConfigDir()`;
   - where the per-project file would live (in the repo? gitignored next to the
     ledger?), and what that means for a fresh clone on another machine;
   - which tables are machine-scoped (sessions, panes, processes, worktree
     locks, jobs, errors) and which are project-scoped;
   - migration of the existing database, and how a schema migration reaches N
     databases.
3. **Order of work**, if a split is recommended: the smallest first step that
   delivers value, and what has to happen before Endless manages a second real
   project (go-cfgstore, gomion).

## Method

- Inventory every table as machine- or project-scoped, from schema.sql.
- Grep Go and Python for queries that join across projects.
- Measure today's database: rows per project and per table.

## Deliverable

An outcome (`task update --status unreviewed --outcome-file`) that gives: the
needed/not-needed verdict with its reasons, the recommended shape if needed,
the table inventory, the cross-project reads that shape breaks and how each is
served, and the follow-up tasks to file. No code, no filing without Mike's
yes.
