# Migrate today's single database into the split

E-2215 child 6.

## Work

- One-time migration: create each registered project's file, copy its project
  tables into it, fill the machine task mirror from them, and drop the project
  tables from the machine file.
- Afterwards, check each project file against a replay of that project's
  ledger and report any differences before dropping anything.
- Take a backup first (`endless-go event backup`).

## Do not carry over (E-2257's findings)

- `project_deps`: 0 rows, no writer.
- `session_statuses`: dead since May; E-2062 notes it is being retired.
- `adhoc_e2191_backlog` and `adhoc_e2191_backlog_v`: made by hand during
  E-2191, not in any schema file.

Ask Mike before dropping any of them. Also ask him whether to retire the stale
or empty tables: `report_labels` (0 rows), `session_gates` and
`report_judgments` (no writes since Aug 23), and `notes` (since Aug 22).
