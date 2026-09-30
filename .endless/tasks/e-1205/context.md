Currently .endless/plans/snapshots/ is a flat directory with <timestamp>-<hash>.{json,md} files written by the PostToolUse hook (cmd/endless-hook/claude.go).

To find all captures for a given task, you have to read each .json's metadata to filter by task ID.
