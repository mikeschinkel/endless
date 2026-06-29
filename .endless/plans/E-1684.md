# session next --tree — order/parallel structure, IDs only

## Why
The flat `session next` list can't show implementation order or which tasks are parallelizable. A `--tree` view makes the structure visible on demand, and is the foundation for later "Claude knows what it can safely run in parallel."

## Behavior
- `endless session next --tree` prints ONLY task IDs in their implementation order, tree-style (à la the `tree` command for directories).
- Initially no titles, legend, block icons, or other columns — keep it minimal.
- Order/parallel structure comes from `session_tasks.order` (sibling task): sequential orders stack; equal orders render as siblings within a parallel group.

## Auto-derived order (key insight)
Order/parallel structure is largely **derivable from the blocked-by DAG**: a task's position = its depth in the `task_deps('blocks')` graph (topological layering); tasks at the same depth with no inter-dependency are parallelizable. So `--tree` can compute the structure automatically from existing blockers — the manual `session_tasks.order` (sibling task) is only needed to express parallelism the blockers don't imply, or to override. This means most "what order?" answers are already latent in the graph; the view surfaces them without Claude re-deriving each time.

## Verification
Deliver `tests/tasks/e-1684-verify.sh` following the established convention (model: `tests/tasks/e-1577-verify.sh`): bash, routes the CLI through the sandbox DB (`--db sandbox`), uses the `section`/`report_pass`/`report_fail`/`summary` + `assert_*` helpers, prints `ALL PASSED` or a `FAILED` list, exits 0 on all-passed / 1 on any failure, runnable from anywhere in the worktree.

Against the sandbox it must verify:
- Build a small dependency chain (e.g. A blocks B, B blocks C, plus an independent D); `session next --tree` renders the IDs in implementation order (A before B before C), derived from the blocked-by DAG.
- Tasks at the same depth with no inter-dependency (a parallel group) render as siblings.
- `--tree` output is IDs-only — no titles, legend, or icons.
- (If `session_tasks.order` from the sibling task E-1683 is present, it overrides the DAG-derived order; otherwise the DAG order is used.)

This needs `session next --tree` to be invocable with an explicit focal task / seeded sandbox session — headless, without a live tmux pane. Confirm that entry point exists or add it.

The verify-handoff instructions for this task must be exactly these two lines, nothing more:

    esu
    ./tests/tasks/e-1684-verify.sh

If that can't be the whole handoff, the script is incomplete — fix the script, not the handoff.

## Depends on
- The session ordering data + command (sibling task) — for overrides / non-blocker parallelism.
- The `session next` read command itself (E-1465).

## Out of scope
- Titles / rich rendering in tree mode — possible later enhancement.
