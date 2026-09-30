Same trigger as E-1510: at the top of cli.main(), scan sys.argv for '--db sandbox' (mirroring DBAwareGroup's pattern); if present AND cwd is inside .endless/worktrees/e-NNN AND Path(__file__).resolve() is NOT already inside that worktree's source tree (re-entrancy guard so the re-exec'd process does not loop), os.execvp into 'uv run --directory <worktree> endless <argv>'.

PATH-prepend in .claude/settings.json was tried and rejected (Claude Code does not expand ${PATH} in env values; see internal/sandboxcmd/bind.go).
