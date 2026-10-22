# Give each project its own database file

E-2215 child 3, shaped by E-2257's findings.

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

- SQLite resolves an unqualified table name in the main schema before any
  attached one. A connection with the machine file as main and the project
  file attached would read the machine mirror for every unqualified `tasks`.
  Pick one convention (the project file as main for project commands, or
  schema-qualified names) and enforce it in a test.
