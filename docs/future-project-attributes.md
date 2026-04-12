# Future Project Attributes

Captured 2026-04-08. To be implemented when building the web interface.

## Priority (1-10)

How important the user views a project currently. Mutable — changes as focus shifts.

## Stage

Lifecycle stage of the project. **Stages should be configurable, not hardcoded** — pull from global config or a DB table so they can evolve over time.

Initial candidates (naming TBD):
- ideation
- proof-of-concept
- development
- mvp
- release (v1.0)
- growth
- maintenance
- archived

## Progress / Headway

Current progress relative to the project's plan phases. Ties into the planning sessions and PLAN.md tracking that Endless already does for document lifecycle.

Possible approaches:
- Percentage complete (simple but lossy)
- Current phase name from PLAN.md (richer but requires parsing)
- Free-text status line (most flexible)

## Implementation Notes

- Add columns to `projects` table: `priority INTEGER`, `stage TEXT`, `progress TEXT`
- Stage values should come from a `stages` table or global config list, not a CHECK constraint
- Priority and stage should be settable via `endless register` flags and editable via a future `endless edit <slug>` command
- Web dashboard should allow drag-to-reorder for priority and dropdown for stage
