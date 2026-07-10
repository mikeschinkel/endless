Implement the rename decision from the field-separation design task: tasks.text -> tasks.plan throughout the schema, ORM, CLI, hook references, and templates.

Surface area:
- DB column rename via ALTER TABLE (schema.sql + migration logic in internal/monitor/db.go).
- CLI flags on task add / task update / task show: --text -> --plan; --text-only display flag stays consistent.
- task show: '## Text' heading -> '## Plan'.
- Hook comment references in cmd/endless-hook/claude.go.
- Web templates (internal/web/pages/task_detail.templ etc.).
- Plan attachment commands: 'endless task update <id> --text <plan-file>' becomes '--plan <plan-file>' (or --plan-file per the file-flag convention task).

Out of scope:
- The decision to separate analysis from plan (parent design task — file-separation).
- File-flag naming convention (separate task — applies orthogonally).

Verification:
- task show on a task with non-empty plan still renders the plan section.
- Existing scripts using --text either continue working via alias or fail loudly. Decide alias vs hard-break in the field-separation design.
- Web task-detail page renders plan content.

Origin: E-989 finalization revealed text-as-plan overloading.