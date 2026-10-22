# Add the machine task mirror as the id allocator

E-2215 child 2, shaped by E-2257's findings.

## Shape

- A mirror of tasks in the machine database, named `_tasks` (EQ-99: no table
  name may exist in both the machine and project schemas; a test enforces it).
  It is the canonical task-id
  allocator (decided under E-2215), replacing `SELECT COALESCE(MAX(id),0)+1 FROM tasks`.
- Fields: `id`, `project_id`, `removed`, `title`, `status`, `phase`,
  `type_id`, `complexity_id`, `risk_id`, `parent_id`, `prime_requested`,
  `created_at`, `updated_at`. That covers every machine-side task read in
  E-2257's inventory. `description` and `verified_sha` are left out.
- Mirror rows are never deleted, only marked `removed`, the same rule as tasks
  (E-1929).
- Machine tables get plain FKs to the mirror with no ON DELETE action (EQ-35):
  - `sessions.task_id`, `epic_id`, `focus_task_id`
  - `session_gates.epic_id`, `task_id`
  - `session_tasks.task_id`, `session_hidden_tasks.task_id`
  - `session_notices.task_id`, `rater_claims.task_id`
  - `session_statuses.task_id`, unless that table is retired first

  Fault tables (`errors`, `errors_sources`, `error_triage`) keep plain INTEGERs
  on purpose.
- A machine-side `task_tree` equivalent over the mirror (`parent_id` plus
  `removed`) gives `effective_parent_id` for session status and tmux.

## Keeping it current

- The executor writes the mirror fields in the same command, right after the
  project write commits. Not atomic: a cross-file transaction isn't atomic in
  WAL (E-2215).
- An idempotent reconcile job repairs drift, and refreshes a project's rows
  when that project is rebuilt.
- No derived flags (`has_plan`, `landed`, open counts) on the mirror. Reads
  that need them use ATTACH (the cross-project reads task).

## Done when

- New task ids come from the mirror.
- Machine-side reads that need only mirror fields read the mirror: tmux pane
  lookup, claim check, the auto-spawn and prime outstanding counts, the
  triage job, the epic walk on claim, the focus update, and the Python session
  list.
