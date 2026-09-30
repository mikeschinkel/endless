A verify.sh suite gets ENDLESS_VERIFY_RUN/TAP/TASK from the runner; a verify.toml [[check]] gets nothing, so a check needing a file from its own suite directory retypes the path.

.endless/tasks/e-1603/verify.toml does: command = "sh .endless/tasks/E-1603/e2e.sh" — uppercase, where the tracked directory is e-1603. It runs only because APFS is case-insensitive; on any case-sensitive filesystem that check cannot find its own script.
