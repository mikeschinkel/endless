# Refuse a second holder of a task, and continue a task only from its earlier session

A session holds a task when its `sessions.task_id` is that task (ED-1560). A task is continued only from the session that held it before, through `session goto E-N --resume`; starting it over with a fresh transcript is the user's explicit `--new-transcript` (E-2106). `task spawn` and `task prime` already enforce this (`_check_task_ownership`, `_check_prior_claim`). This task makes `task claim`, `task bind` and the SessionStart cwd auto-bind enforce it too.

## claim and bind
- `claim_item` and `bind_item` (`src/endless/task_cmd.py`) refuse E-N when any other session holds it or held it, present or ended, by the same test `_check_prior_claim` applies. The refusal changes nothing and names the most recent holder and `endless session goto E-N --resume`. `--new-transcript` appears only in the refusal's human remedy, never in the text an agent reads, as in `_require_transcript`.
- A session claiming or binding its own task again is not refused.

## The SessionStart cwd auto-bind (`autoBindFromCwd`, `internal/hookcmd/claude.go`)
Two new checks, after the existing worktree-lock check:
1. Another PRESENT session, in a different tmux pane, holds the task: do not bind. Record a fault. Return JSON with `additionalContext` telling the agent, and `systemMessage` telling the user, that E-N is held by ES-M: run `endless session goto E-N`, and close this session.
2. The SessionStart `source` is `startup` and an ENDED session held the task: do not bind, unless the launch was made by `--new-transcript`. Tell the agent and the user the same way to run `endless session goto E-N --resume`. No fault: this is a user starting `claude` by hand, not a defect.
- `source` `resume`, `clear` or `compact` continues the same conversation line and binds as today. A holder in the same pane is this pane's own previous session.
- `--new-transcript` (`session resume` and `session goto --resume`) marks its launch so the new session's SessionStart knows the fresh start was asked for: an environment variable on the launched `claude` command naming the task. `task spawn`'s launch needs no mark: `_check_prior_claim` has already found no earlier holder.

## The board
- Remove the `claimers >= 2` case from `AnnotateSessionStatusStewardship` (`internal/monitor/session_stewardship.go`): two present holders can no longer arise. One present holder still clears the duplicate mark.

## Guide
- `endless guide tasks` (claim, bind) and `endless guide sessions`: a task is continued only from its earlier session; a fresh transcript is the user's `--new-transcript`.
