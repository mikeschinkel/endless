# Plan: Refuse to land a task with an unfinished blocker

Land refuses a task while any task that blocks it is not terminal, using the same rule, wording and remedy as claim, spawn and prime.

## Approach

- In `land_worktree` (`src/endless/worktree_cmd.py`), right after the existing verify gate (`unlanded.require_landable`) and before the `--dry-run` report, call E-2296's `_require_unblocked(item_id, "land")` from `src/endless/task_cmd.py`. It already reads every `blocked_by` link through `_live_blockers`, releases only on a terminal status, and raises the refusal that names each blocker with an `endless task unblock E-N --by E-M` line. If importing it into `worktree_cmd` makes an import cycle, move `_live_blockers` and `_require_unblocked` to a module both can import, and keep the claim, spawn and prime call sites unchanged.
- Skip the check when the task's branch is already an ancestor of its base branch: a re-run after a failed build or migration, whose code is already in main.
- `--record-only` dispatches before the check, as it already dispatches before the verify gate.
- `just land` runs `endless worktree land`, so it is covered without a change to the recipe.

## Tests

In `tests/`, against real git repositories:
- a land of a task with a non-terminal blocker refuses, names the blocker and the unblock command, and leaves main, the branch and the task's status unchanged; the same for `--dry-run`;
- a blocker in another project refuses the same way;
- a `landed` blocker still refuses;
- a terminal blocker (one of `assumed` and `declined`) does not;
- a re-run whose branch is already merged into its base proceeds past the check;
- `--record-only` is not refused.
