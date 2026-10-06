# Classify every table for the storage split

The first child of the split: everything else (mirrors, path resolution,
migration, cross-project reads) is built from this classification.

## Scope

`internal/schema/schema.sql` — 34 tables, 2 views, 1 FTS virtual table as of
2026-10-06 — plus any table a goose migration adds that schema.sql lacks.

## Method

1. For each table, record:
   - **scope**: machine (sessions, panes, processes, locks, jobs, errors, …)
     or project (tasks, decisions, relations, landings, task content, …);
   - **rebuildable from a project ledger?** yes / no / partly — a
     project-scoped table that is not fully rebuilt by replay is a gap for
     the "emit events for mutations the projection must rebuild" work, not
     for this task to fix;
   - **outbound foreign keys** that would cross files after the split.
2. Every machine → project foreign key names a **mirror table** the machine
   database must keep (per ED-1602: per-kind, not one registry). For each
   mirror, list the fields worth syncing (id, project, title, status, …)
   and which machine queries need them.
3. Every project → machine foreign key: list it and propose how it is
   carried (keep the value without the constraint, move the column, or
   drop it).
4. Views and FTS: say which file each belongs to, and whether any must be
   rebuilt as a cross-file view over ATTACH.
5. Note any table whose scope is genuinely mixed (rows of both kinds) and
   propose a split of that table.

## Deliverable

An outcome holding the classification table and the lists from steps 2–5,
plus the follow-up children of the split it implies (mirror tables +
reconcile job; anything mixed). Changes no code.
