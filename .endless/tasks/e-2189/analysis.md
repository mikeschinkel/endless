Why the full rename: renaming only the command would leave it writing a relation with a different name, which splits the vocabulary in a new place instead of removing the split. 'Replace' also reads as swapping content in place.

Surface to cover:
- CLI: 'task replace' becomes 'task supersede <old> --by <new>', with 'replace' kept as a hidden alias; the --replaces flag on task add/update.
- Relation vocabulary: stored dep_type 'replaces' becomes 'supersedes'; display names replaces/replaced_by become supersedes/superseded_by (CANONICAL_DEP_TYPES, STORED_DEP_TYPES, RELATION_LABELS, RELATION_DISPLAY_ORDER in task_cmd.py).
- Rendering: task show's '(replaced by E-N)' status note, the JSON replaced_by key, the agent replaced_by= token, and the NOT-authoritative banner.
- Data: migrate existing task_deps rows. The ledger is immutable, so events carrying dep_type 'replaces' must still replay, via an alias on the read path (the pattern tasktype.Parse uses for 'task'/'bug').
- Docs: guide tasks.md and index.md, and the superseded status description.