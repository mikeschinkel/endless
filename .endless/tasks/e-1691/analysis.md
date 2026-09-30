Mirror E-1685: a read-time UNION over tasks WHERE parent_id = that task AND status non-terminal; do NOT write them to session_tasks (derived).

DIRECT children only (decided 2026-06-30): working a child surfaces ITS children in that child's own session, keeping each session's view one level deep instead of exploding the whole subtree.

Being a row-set addition it also flows into --tree (children nest under that task in the spine).
