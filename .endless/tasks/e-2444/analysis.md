Settled in E-2439 (do not re-open while planning):

- A post-land item is a plan item whose work needs the landed, migrated, locally installed result and produces no code. Work that would produce code is a follow-up task, not a post-land item.
- Deferral mark: set on a deliverable item while planning (approved with the plan), or set at reconcile when an unforeseen item is reconciled `blocked` and the user's answer is the structured choice "do it after land".
- New status `landed`: `unlanded → landed` by the land when deferred items remain (otherwise the land goes to `assumed` as today); `landed → assumed` by the reconcile that records the last deferred verdict; `landed → revisit` when shipped work is wrong; `landed → declined/obsolete` by the user. No `landed → confirmed`.
- `task reconcile` accepts a `landed` task for its deferred items only, in a file holding just those entries; each run records a new reconciliation. Reconcile stays the single writer of verdicts.
- The agent records the post-land verdict, attributed to its session. The verdict carries a note; the item's `verify:` line names the check.
- A failed post-land verdict keeps the task `landed`; a later reconcile records the pass.
- Gate: reconcile on a `landed` task refuses when the task branch has commits past the land, and the refusal names the remedy (file a follow-up with --cleans-up).

Agent-facing surfaces (all required):
- Reconcile output when an item is deferred: the land will move the task to `landed`; names the reconcile to run then.
- Reconcile output on a failed post-land verdict: the task stays `landed`; re-run reconcile when the item passes.
- Per-prompt hook context for a `landed` task: lists open post-land items (the land runs in another pane, so the agent never sees land's output).
- `task show` and the task list show open deferred items on a `landed` task.
- Guide: content goes in an existing section (tasks), as a subsection `endless guide tasks/post-land` once E-2443 exists; messages above point to it by name.

Status-diagram upkeep: add `landed` and its edges to internal/taskstatus/transitions.go and run `just lifecycle-index`, which regenerates docs/status-lifecycle.mmd and its copies in README.md and docs/guide/index.md; `just test` fails on drift.
