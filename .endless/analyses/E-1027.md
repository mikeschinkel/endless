The companion file (E-989) has a worktree_path field that is currently never set — writeClaudeCompanion always leaves it empty. This makes session use (E-1014) and session cd (E-990) fall back to cwd, which for sessions started outside a worktree (like the bootstrap session running in main) does not point at the worktree the user actually wants to test.

Goal: when a Claude session has an active task with an associated worktree, the companion file's worktree_path reflects that worktree. Readers (session use, session cd, session show) then resolve to the right place automatically, no extra logic in each command.

Where to write it:

Option A — writer side, hook-driven. In cmd/endless-hook/claude.go, on PostToolUse (or UserPromptSubmit) after the session's active_task_id changes (e.g. via 'endless task start'), look up whether .endless/worktrees/e-<task_id>*/ exists; if yes, update the companion file's worktree_path. Single source of truth, all readers benefit.

Option B — reader side, on demand. In session_use_resolve / session_cd_resolve / session_show_resolve, after fetching the companion record, query DB for active_task_id and check filesystem for the worktree. Skip the companion field entirely.

Trade-offs:
- A keeps readers simple, costs a hook change and a small write per task switch.
- B avoids hook churn but duplicates the lookup in every reader.

Recommend A: cleaner long-term, fewer places to keep in sync. The hook-side change is small (a few lines after handlePostToolUseSession's task-start branch).

Worktree naming convention is the filesystem glob '.endless/worktrees/e-<task_id>*' — agreed convention from E-989/E-990 work, formalized in E-971 (which is still blocked but the convention is in active use).

Verification:
- Start a Claude session, run 'endless task start <id>' on a task with a worktree at .endless/worktrees/e-<id>-<slug>/. The companion file's worktree_path updates to that path within the same UserPromptSubmit cycle.
- Switch active task via another 'endless task start <other-id>'. worktree_path updates to the new worktree (or clears if the new task has none).
- 'endless session use' on that session emits cd to the worktree, not the cwd.

Out of scope:
- Formalizing the worktree-naming convention beyond filesystem glob (E-971's domain).
- Cleaning up worktree_path when a task ends (covered by SessionEnd removing the whole companion file).

Origin: E-1014 landing surfaced that worktree_path is dead today. Without this, session use's activation lands at cwd, which for sessions started outside a worktree is the wrong place.