Today `esu` (from `endless shell-init`) takes no argument and resolves the
current session's worktree. Agents (and Mike) repeatedly type `esu e-NNNN`
because the task id is the natural thing to name; it's currently invalid.
Proposal: let `esu [<task-id>]` accept an optional task id — bare `esu`
keeps today's behavior; `esu e-1655` cd's into that task's worktree and
exports its ENDLESS_SESSION_ID, erroring clearly if the id names no real
session/worktree.

Decide first: this overlaps E-1180 ("Add eswt shell function to switch to a
task worktree"), which proposes a *separate* `eswt <task-id>` helper for the
same capability. Resolve whether to (a) fold E-1180 into this by extending
`esu`, or (b) keep `eswt` distinct and close this. Don't implement both.

DO NOT CHOOSE — present to Mike and have him decide the esu-vs-eswt split
before planning.
