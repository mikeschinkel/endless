topic: commit-to-main policy
section: orchestration
covers: When to commit to main vs work only in a worktree.

topic: committing your work
section: orchestration
covers: You commit your own changes on the task branch; endless commits only its own files.

topic: who am I / current session
section: sessions
covers: Discovering your session id and the task it's bound to.

topic: preference vs prohibition
section: decisions
covers: Soft signals ('ideally','usually') are not rules - verify before recording.

topic: the handoff (generated, not authored)
section: orchestration
covers: Spawned sessions get a rendered handoff; agents never write it.

topic: worktree DB sandbox (--db main vs sandbox)
section: orchestration
covers: Self-dev DB routing and the --db choice.

topic: shell helpers (esu / esp / esf)
section: orchestration
covers: cd into your worktree and export ENDLESS_SESSION_ID.

topic: blocking semantics
section: tasks
covers: How unverified/unreviewed/confirmed/assumed/completed affect whether a blocker is still active.

topic: verbs
section: tasks
covers: The registered action words that can begin a task title.

topic: research-task field model
section: tasks
covers: For a research task, text = the request, outcome = the deliverable.

topic: per-task verification suite
section: orchestration
covers: One suite per task and the one-command verify handoff (`endless task verify`, .endless/tasks/e-*/).

topic: commit message convention
section: orchestration
covers: Commit subjects on a task branch take the form E-<id>: verb-first summary.

topic: landing is the user's call (ask first)
section: orchestration
covers: Never run worktree land or drop on your own initiative - ask every time.

topic: work you discover mid-task (do it, reopen, or file it)
section: tasks
covers: Four-case test for a drive-by discovery: do it now, reopen your own landed work, file it with --cleans-up, or fold symptoms into one root cause.

topic: lean toward fewer tasks
section: tasks
covers: Prefer one task over several - every filed task spends the user's review attention.

topic: $FULL
section: tasks
when: report_gate
covers: The sigil licenses one response that bypasses the minimizer entirely, not a sticky mode.

topic: $CUT / $BLOAT / $WRONG / $GOOD
section: tasks
when: report_gate
covers: The four labels that annotate the preceding turn and build the minimizer's eval corpus.

topic: --keep-status (edit the content, infer nothing)
section: tasks
covers: Suppressing every status auto-transition that task update infers from an edit.
