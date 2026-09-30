Workstream 1 of E-968.

Two pieces shipped (commit f1e953d): (1) PostToolUse hook in cmd/endless-hook/claude.go writes a content-addressed snapshot to .endless/plans/snapshots/<ts>-<sha8>.{md,json} on every Write to ~/.claude/plans/*; (2) endless task update --text additionally writes a stable per-task copy to .endless/plans/<task-id>.md (DB stays canonical; file is a predictable export). New CLI: 'endless plan-snapshots {list,show}' with --session/--today/--json filters.

Pruning deferred to E-984.
