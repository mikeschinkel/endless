The Claude Code hook (cmd/endless-hook/claude.go) needs to read @endless_plan_id from the tmux window option set by endless spawn.

This also covers wiring up the ExitPlanMode event to trigger import instead of PostToolUse/Write.
