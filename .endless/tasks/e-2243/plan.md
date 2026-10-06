Decisions (settled with Mike):
- Record only passing runs. A verify suite is a land-time gate for one task; a failure matters only within the worktree's lifespan.
- One file per passing run, flat in the task's directory: `.endless/tasks/e-NNNN/verify-<UTC timestamp>-<short HEAD sha>.ctrf.json`. No subdirectory: one to three files per task.
- `endless task verify` refuses unless the worktree is fully committed, so every report's SHA is exactly the code it tested.
- The runner (`endless-go verify`) stays unaware of git, so it can be extracted as a standalone tool. The Endless layer (`endless task verify`) does the clean-worktree check and the commit.
- One new commit per recorded run, never amended.
- If the report cannot be committed, the verify command fails: something is wrong and must be resolved, not papered over.
- The committed file is the record. No ledger event and no database row.
- The runner keeps writing its report to the user cache, unchanged. Endless offers no option to put the report anywhere but its standardized location; such an option may come with the extracted standalone tool, not now. The cache copy is only the handoff the wrapper copies from.
- Always on for every project; reports are kept forever.

1. Clean-worktree gate (src/endless/verify_cmd.py, before invoking endless-go): run `git status --porcelain` in the task's worktree and refuse when anything is listed outside the files Endless itself writes there (pending `.endless/verbs.jsonl` additions; anything gitignored is already excluded). Name the files and say to commit them, then verify again. Nothing runs.

2. Commit verb (Go, beside `CommitDoc` in internal/events/commit.go): a function that commits one named file on the project's main checkout through `commitPaths`, so it inherits main-checkout enforcement, the index.lock retry, git-env stripping, and leaving anything else that is staged alone. It must always make a new commit: give each commit a subject unique to the run (e.g. `Endless: verify E-NNNN <filename>`), and keep that subject out of AMENDABLE_COMMIT_SUBJECTS so land's orphan-strip never treats it as amendable. Expose it as an `endless-go` subcommand the Python wrapper calls.

3. Wrapper flow (src/endless/verify_cmd.py): on runner exit 0 (all passed), copy the report from the runner's cache path into the main checkout at the path above and call the commit verb in the same step, so main is dirty only for the copy-then-commit. On a failing run, record nothing. If the copy or commit fails, exit non-zero with a refusal naming the cause, even though the suite passed. Print the committed path.

4. Docs: `.endless/tasks/CLAUDE.md` gains a third kind of file in a task's directory: verify-run reports, written by `endless task verify` and committed on main, never hand-edited or `git add`ed. Update the verify section of `endless guide orchestration` with the clean-worktree requirement and where reports go.

5. Tests:
   - Go: the commit verb makes a new commit per call (never amends, even after an earlier run's commit), refuses when the target is not the main checkout, and leaves unrelated staged files alone.
   - Python: a dirty worktree is refused before anything runs; an Endless-managed-only change is not refused; a passing run commits exactly one report at the expected path on main; a failing run commits nothing; a commit failure makes the command exit non-zero.
