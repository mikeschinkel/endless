`validate_title` (src/endless/task_cmd.py) checks only that a title begins with a registered actionable verb from the verb catalog — nothing checks the verb against the task TYPE.

So a brainstorm, whose deliverable is a decision, can be titled with a doing-verb and read as implementation work:

E-1958 was filed as "Make per-worktree sandboxes available to all projects" and passed validation cleanly, when a brainstorm should read "Decide how...".

The title is what everyone scans in `task next`, `task list` and the dashboard, so a mismatched verb misrepresents what the task will produce and invites a session to start building instead of deciding.
