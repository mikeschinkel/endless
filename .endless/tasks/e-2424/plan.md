# Give each project its own database file

E-2215 children 3, 5 and 6 as one cutover, shaped by E-2257's findings.
E-2425 (migrations) and E-2427 (moving the data) were folded in on Mike's
decision, 2026-10-22, applying ED-1550: none of the three can go live without
the others.

## Shape

- `~/.config/endless/projects/<slug>.db`, named by E-2217's machine-local
  slug. Under self_dev the sandbox mirrors this layout under its own config dir.
- **Project file tables:** `tasks` (with `live_tasks`, `task_tree`),
  `task_content`, `task_questions`, `task_deps`, `task_landings`,
  `decisions`, `decision_relations`. Seeded enum tables `task_types`,
  `complexity_levels` and `risk_levels` go in both files.
- **Machine file tables:** everything else (see E-2257's outcome).
- **Project identity:** a one-row table holding the committed identity
  (root commit, per E-2217) and the committed name, written by replay. It
  replaces the projector's `ensureProject`.
- **`project_id`:** `tasks.project_id` and `decisions.project_id` hold the
  machine `projects.id`, stamped when the file is built. No FK across files.
- **Project → machine FKs** lose their constraint and keep the value:
  `task_landings.session_id`, `decisions.origin_session_id`.
  `tasks.changed_by_session` and `task_questions.asked_by_session` are already
  plain.
- **Resolution:** route through the `--db main|sandbox` context (E-2059), so
  every command opens the machine file plus the right project file.
- **Rebuild:** a project rebuild deletes and rebuilds only that project's file
  from its ledger.

## Watch for

- The machine file is main, and project files attach to it: one connection
  can hold several project files at once, within SQLite's attach limit.
- The mirror is named `_tasks` (EQ-99 on E-2423), so no table name exists in
  both the machine and project schemas. Reads that touch project tables name
  the attached database they read, because every project file has a `tasks`
  and SQLite silently picks the first attached one.

## Migrations across files (was E-2425)

### Work

- Split the goose migration set into machine migrations and project
  migrations. Each file keeps its own `goose_db_version`.
- Apply project migrations to every project file under ConfigDir() and its
  self_dev sandbox counterpart. Apply machine migrations to the machine file.
- Split `schema.sql` along the same line, or generate it per file if E-2021
  lands first. Keep `TestMigrate_MatchesSchemaSQL` passing for each file.
- Coordinate with E-2034 (versioned migrations instead of apply-on-connect)
  and E-2021 (schema.sql generated from goose).

## Move today's data (was E-2427)

### Work

- One-time migration: create each registered project's file, copy its project
  tables into it, fill the machine task mirror from them, and drop the project
  tables from the machine file.
- Afterwards, check each project file against a replay of that project's
  ledger and report any differences before dropping anything.
- Take a backup first (`endless-go event backup`).

### Don't carry over (E-2257's findings)

- `project_deps`: 0 rows, no writer.
- `session_statuses`: dead since May; E-2062 notes it is being retired.
- `adhoc_e2191_backlog` and `adhoc_e2191_backlog_v`: made by hand during
  E-2191, not in any schema file.

Ask Mike before dropping any of them. Also ask him whether to retire the stale
or empty tables: `report_labels` (0 rows), `session_gates` and
`report_judgments` (no writes since Aug 23), and `notes` (since Aug 22).
