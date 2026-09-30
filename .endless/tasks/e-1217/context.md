Other task subcommands (add, list, detail, link) work from any cwd — they default to a registered project.

`endless task update` is the outlier: when cwd isn't in a registered project directory (e.g., shell cwd dangles after `git worktree remove`), it errors with 'Not in a registered project directory. Specify a name: endless task <command> --project <name>' but does NOT actually accept --project.

Observed during E-1186 wrap-up 2026-05-09 after the worktree dir was removed mid-session.
