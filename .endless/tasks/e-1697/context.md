Root cause (proven): SelfDetectWorktreeSandbox() calls SetDBContextDir(sandbox), making HasExplicitDBContext() true, which makes cmd/endless-go/main.go skip PinMainDB() for the tmux/channel subcommands - so status-line reads the worktree sandbox DB where no session rows exist.

Sessions live only in main.

esu is the same class on the Python side (CLI self-routes session reads to sandbox).
