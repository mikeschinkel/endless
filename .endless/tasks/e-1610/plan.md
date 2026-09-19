# Implement ED-1509: epic child handoffs read parent analysis + guide section

Implements ED-1509 (epic content-field convention). Two pieces, both applying to ALL epics.

## 1. Handoff: a child of an epic is pointed at the parent's analysis

When a spawned task's parent is an `epic`, its handoff must instruct the child to read the
parent epic's shared design/contracts:

`endless task show E-<parent> --analysis --db main`

- Templates: `internal/templatecmd/templates/handoff/{task,bug,research}.md.tmpl` (the child
  types). Add a conditional block rendered only when the parent is an epic.
- Renderer: `render_handoff` (`src/endless/task_cmd.py` ~3501) must pass the parent's id and
  whether the parent is an epic into the template data.
- The line sits alongside the existing "read your plan (`--text`)" step.

## 2. Guide: document the epic content-field convention

Add a section to `endless guide` (likely `docs/guide/tasks.md`) documenting the convention from
ED-1509: research child `outcome` = evidence; epic `analysis` = vision/decisions/interface
contracts; epic `text` = roadmap; each child `text` references the epic's analysis. Include the
inheritance rule and the interim "sections in analysis" storage note.

## Verify

- Spawn a child under an epic; its handoff contains the `--analysis` read line. Spawn a child
  under a non-epic; it does not.
- `endless guide tasks` shows the epic content-field convention section.

## Note (forward)

Storage is interim (sections in `analysis`). When E-1531 (task_content) lands, the guide section
and any field references update to the purpose-specific content types (e.g. a `design` type) per
ED-1509's forward direction.
