endless task update --text always writes plan files to main's .endless/plans/ via _write_task_plan_file -> _project_root_for_task (task_cmd.py), regardless of cwd or active worktree.

This bit us in 2026-05 when another session created plan files for E-1164/E-1182/E-1186/E-1190 on main while working in worktrees, blocking subsequent landings of E-1170 and E-1197 with stash dances that risked data loss.
