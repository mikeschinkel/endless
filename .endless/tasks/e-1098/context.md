Current isPlanFile (cmd/endless-hook/claude.go) only matches paths under /.claude/plans/ or paths ending in /plan.md (case-insensitive).

Per session experience, Claude writes plan-shaped uppercase markdown like PLAN_FOR_FOO.md and FOO_PLAN.md that the hook silently ignores.
