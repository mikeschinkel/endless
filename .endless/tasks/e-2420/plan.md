# Keep Claude transcripts indefinitely, or a user duration

Claude Code deletes transcripts older than its `cleanupPeriodDays` (default 30, minimum 1, no "never" value) in a background pass after any session starts, across every project. Endless makes sure the user's Claude settings keep them for the Endless retention value.

## The Endless retention value
- `claude.transcripts.retention_days` in the machine config (`~/.config/endless/config.json`), a whole number of days of at least 1. When unset it is 3650, which is effectively indefinite.
- Read in Go (`internal/config`, a CLI-layer field like `Tmux`) and in Python (`config.load_config`) from the same key.

## endless setup claude-hook
- After the hooks, it compares `cleanupPeriodDays` in the user's Claude settings file with the Endless value. The file is `settings.json` under Claude's home, which is `CLAUDE_CONFIG_DIR` when set, else `~/.claude` (setup's `CLAUDE_SETTINGS_PATH` gains the same override `session_cmd` already honors).
- Unset or lower: show the change and ask, then write it.
- Equal or higher: leave it. A higher value is reported, never lowered, since lowering deletes transcripts at the next session start.
- A managed settings file that sets a lower value cannot be overridden: setup writes nothing for it and names the managed value.
- `--yes` applies every change setup would ask about, hooks and retention, without asking.

## SessionStart warning
- The Claude SessionStart hook reads the same value (managed first, then the user's file) and, when it is below the Endless value, returns a `systemMessage` naming the current value, the Endless value and `endless setup claude-hook`. Nothing is shown when the value is at or above it.

## Doctor check
- Registered in the check registry E-2285 builds: a value below the Endless value is a problem, fixable with confirmation by the same write setup makes. Blocked by E-2285.

## Guide
- Document `claude.transcripts.retention_days`, what setup changes, and the warning.
