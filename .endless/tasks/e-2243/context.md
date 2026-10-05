`endless task verify` writes each run's merged CTRF report to the user cache dir, at `<cache>/endless/verify/<task>/ctrf.json` (internal/verifycmd/report.go, `writeCTRF`), and overwrites it on the next run. So:

- Nothing durable records that a task's suite ran, what it ran against, or whether it passed. Confirmation records the person's verdict, not the suite's.
- The report exists only on the machine that ran it, and only until the next run.
- CTRF was chosen because it is a standard. Inside the runner that already pays off: TAP, pytest and `go test` output become one schema. The other half, any CTRF-aware tool (reporters, CI annotations, dashboards, trend charts) reading the history, needs the reports kept as files, and today none are.

Committing every run, rather than choosing one per task, is deliberate. A history needs no step that picks "the last run" and can fail to record it: the final run before a land is simply the newest entry.

Scope is per-task verify suites only. Whether full test-suite runs (`just test`, about 4,000 tests) should be recorded the same way is a separate question with different size and noise trade-offs, and is not part of this task.

Open questions for the plan:
- Where the reports live. `.endless/tasks/CLAUDE.md` names two kinds of file under a task's directory: the task's own (written on its branch) and database mirrors (written on main). A committed run report would be a third kind, so that file and the guide need to say so.
- Naming, so each report says which code it tested: e.g. a timestamp plus the tree SHA, with a flag for a dirty tree.
- How the commit reaches main from a run in a worktree, through the same auto-commit path Endless uses for its own files on main, so a green run cannot exist without its record.
- Whether a ledger event points at each report, so "was this verified, and against what?" is answerable through the database.
