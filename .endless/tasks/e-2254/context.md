From the E-2250 brainstorm (Mike, 2026-10-06). One task for the whole change, per ED-1550; the three parts below were filed separately at first, as E-2254, E-2255 and E-2256.

## Type rules (ED-1610, ED-1611)

An epic carries objective and strategy rows, typically never a plan. Both are required to claim or spawn, not to file. Today _require_spawnable (src/endless/task_cmd.py) demands a plan of an epic. group is its own type: completion derives from its children, epics and groups never nest in each other, a group can be changed to an epic (rare), and a group can be claimed and spawned with a session that only coordinates, so it needs its own claim handoff. New rows are hidden in task show unless asked for (E-2252).

## Display (ED-1612)

Today taskrow.Classify (internal/taskrow/taskrow.go) maps status to action without looking at type. So an epic shows the plan pencil whenever any child is unplanned (E-1537 rule 3), even when the epic has a plan (E-2215, E-799), and Mike misses the E and asks for planning or verification.

- Epic: unplanned (pencil; the epic lacks an objective or strategy), submitted, underway, complete.
- Group: submitted, underway, complete.

These are display names computed from the container's own rows and its children, not new statuses, and each gets its own glyph. project status (internal/projectstatuscmd/render.go) has urgent, epics and other sections today. It and the session monitor get four: urgent, epics, groups, other.

## Sorting the open epics

An agent proposes which open epics become groups, and Mike approves the split. Existing epic plans move into objective and strategy case by case; a plan that fits neither stays as a legacy plan row. E-1738 has no children yet but is waiting on future ones. E-1790 is already a todo related to E-1596.
