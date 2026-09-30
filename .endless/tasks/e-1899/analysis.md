Both flags forward to one renderer (epic_cmd.list_epics wraps task_cmd.show_plan), so there is a single branch and a single glyph map to delete.

The flat-list tree seemed useful when added but has gone unused; what is wanted instead is an interactive TUI tree, which is much larger scope and deliberately unfiled.

Do NOT touch 'session status --tree' or 'session monitor --tree' — those are Go-backed, are the trees actually in use, and share only the flag name.

See the plan for the exact site list and the second, unrelated glyph map that must survive.
