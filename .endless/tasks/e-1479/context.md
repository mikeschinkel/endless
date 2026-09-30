When a worktree was landed manually (or before task_landings existed), there's no clean path today to register the task.landed event after the fact.

First use case: backfill the missing task_landings row for E-1402, which landed on 2026-05-17 before the table existed.
