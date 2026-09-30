monitor.hasLiveProcessInDir — the "is a process standing in this worktree" half of monitor.WorktreeInUse (E-1947) — shells to `lsof -t -a -d cwd +D <dir>`.

Unreachable on macOS (lsof ships at /usr/sbin/lsof), but Linux is now real: the first beta user can run Endless there.
