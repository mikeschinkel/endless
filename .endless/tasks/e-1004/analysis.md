Bug surfaced during E-957 implementation (noted in .endless/plans/E-968.md, W1 section, 2026-04-29 addendum).

When 'endless task update --text' runs from inside a git worktree of the project, the per-task plan-file mirror lands in main's checkout, not the worktree's. Today _project_root_for_task() (src/endless/task_cmd.py, added by E-969) reads the project's path from the projects table — which is fixed at registration time and points at main. From inside a worktree, the file ends up invisible to the worktree's git history until manually copied.

Resolution rule (from the plan):
1. If cwd is inside a registered project's main checkout → return that path (current behavior).
2. If cwd is inside a git worktree of a registered project (detect via 'git rev-parse --git-common-dir' matching the project's .git directory) → return the worktree's root path, NOT main.
3. If neither applies → fall back to DB-stored path with a warning.

Critical file: src/endless/task_cmd.py — _project_root_for_task() (added by E-969).

Verification: from inside a worktree, run 'endless task update E-X --text <path>'. Confirm .endless/plans/E-X.md appears in the worktree's checkout, not main's.