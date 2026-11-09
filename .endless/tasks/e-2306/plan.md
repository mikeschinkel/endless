# Plan: let task spawn resume a task's primed session

Terms:
- **Primed session** (ED-1621): a task's implementation session started ahead of need. It reads the task in, runs `endless session primed`, and holds in session state `primed`.
- **Primer**: the session that ran `endless task prime`. A prime started by the prime job has no primer.
- **Live owner**: a live session whose `sessions.task_id` is the task.

## 1. Spawn on a primed task switches to the primed session
In `spawn_item` (`src/endless/task_cmd.py`), before `_check_task_ownership`, `_check_prior_claim` and `_require_spawnable`, look up the task's live owner. When that owner is in session state `primed`:
- Switch the tmux client to the owner's pane through `session goto`'s helpers (`_tmux_switch_client` with a back-stack push), so `endless session back` returns.
- Do not create a window, worktree or status change. The task stays pre-work, and the user's next message in that window resumes the session through the existing `resumePrimedSession` hook, which tells it to claim.
- Do not type anything into the pane (ED-1615).
- Record `spawned` on the session that ran spawn, through `_record_spawned`, as every spawn from a session does.
- Print: "E-N is held by its primed session ES-X; switched to its window. Your next message resumes it."
- `--no-refocus`: skip the switch and print the window to select instead.
- `--worktree`, `--model`, `--tmux-session` and the placement options shape a new window, so spawn prints that they had no effect.

The primed check runs before the plan check because a primed session may still be waiting on answers to its questions, and that session is where they are answered.

## 2. The other live-owner refusal names a working route
`_check_task_ownership`'s refusal for a live owner in any other state drops "have it release the task" (impossible: `sessions.task_id` is write-once, ED-1560) and names `endless session goto E-N`. Spawn, claim and every other caller share that refusal text.

## 3. A `primed` session-task relation recorded on the primer
- `internal/sessiontaskrelation`: append `RelationPrimed` with id 8, slug `primed`, label `Primed`, under the never-renumber rule. Rank it just below `spawned`, so a later spawn from the primer upgrades the row to `spawned`. Add it to `All`, `Parse`, the seed rows and `VerifyIntegrity`.
- `internal/events`: a new kind, `session_tasks.primed`, whose executor upserts `primed` through `sessiontaskrelation.Record`, modelled on `session_tasks.spawned`.
- `task prime` (`prime_item`) emits it for the primer's session after the window opens, best-effort with a warning like `_record_spawned`'s. The prime job and a prime from a plain shell record nothing.
- `session status` (`relation_tier.go`, `session_status.go`): `primed` takes `spawned`'s tier and drop-off rule, and wears the ◇ mark (the glyph of the `primed` session state) in the relation column.
- Stewardship (`session_stewardship.go`) counts `primed` as it counts `spawned`: on the filer rung.

## 4. `task show` says a task is primed
When the task's live owner is in session state `primed`, the `Status:` line reads `Status: <status>; primed in ES-N`. Only the part before the semicolon is the task's status (ED-1621: a task status is never "primed"). The suffix goes when the session resumes or ends. Machine formats (`--json`, `--agent`) keep `status` as the bare status.

## 5. Docs
- `endless guide orchestration`, **Primed sessions**: resuming by `task spawn`, and the primer's `primed` row.
- `endless guide sessions`: the `primed` relation in the relation list and its ◇ mark.
- `endless guide tasks`: the Status-line suffix.
- `task spawn` and `task prime` help and docstrings.

## Tests
- Go: the relation enum, rank and seed integrity with `primed`; the `session_tasks.primed` executor; `spawned` upgrades `primed`; `session status` shows the ◇ mark; stewardship counts a primer.
- Python: spawn on a task whose live owner is `primed` switches and creates nothing; spawn on a task whose live owner is `working` is refused with the `session goto` route; `task prime` from a session emits `session_tasks.primed`; `task show` prints the suffix only while the owner is `primed`; a primed session's row in `task show`'s Sessions block reads `Bound`, not `Claimed`.
