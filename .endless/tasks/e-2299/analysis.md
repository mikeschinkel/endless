Scope, per Mike on 2026-10-17: every place that looks up an existing task worktree must find it in the task's own project repo, not just resume. E-2370 fixes the creation side by resolving the repo from `tasks.project_id` inside both worktree creators. This task is the matching lookup side. It should use the same `task_repo_root(task_id)` resolver that E-2370 adds, inside the lookup functions, so callers cannot pass the wrong project.

Lookup sites found on 2026-10-17 (grep for `WorktreePathForTask(`, `worktreePathForTaskAtRoot(`, `_worktree_path_for_task(`):
- Go `internal/monitor/worktree.go` `WorktreePathForTask(projectID, taskID)` takes the caller's projectID, usually the session's. Callers:
  - `monitor/resume.go` (resume-target)
  - `hookcmd/primed.go`
  - `hookcmd/claude.go` (two sites)
  - `hookcmd/write_target.go`
  - `monitor/worktree_unsettled.go`
  - `monitor/worktree_anomalies.go`
  - `claimhandoffcmd/claimhandoff.go`
  - `monitor/worktree_paths_cache.go` (via `worktreePathForTaskAtRoot`)
- Python `session_cmd._worktree_path_for_task(project_root, task_id)`. Callers: `session_cmd._live_sessions` (cwd project root) and `task_cmd` (one site). `worktree for-task` also resolves a task's worktree.
- Branch and landing checks used to classify a missing worktree in resume. E-2370 moves resume's classification to the task's repo. Any other branch-existence check keyed by task should follow.

Not lookup by task id, so out of scope: cwd-derived "which worktree am I in" helpers (`config.py` gated_worktree_root, `monitor.WorktreeRoot`, `sandboxpath.go`). The reapers walk a given project's `.endless/worktrees` directory.
