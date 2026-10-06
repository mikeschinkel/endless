Decisions (settled with Mike):
- Commit only passing runs. A verify suite is a land-time gate for one task; a failure matters only within the worktree's lifespan.
- One file per passing run, flat in the task's directory: `.endless/tasks/e-NNNN/verify-<UTC timestamp>-<short HEAD sha>.ctrf.json`. No subdirectory: one to three files per task.
- `endless task verify` refuses unless the worktree is fully committed, so every report's SHA is exactly the code it tested.
- The runner (`endless-go verify`) stays unaware of git, so it can be extracted as a standalone tool. The Endless layer (`endless task verify`) does the clean-worktree check and the commit.
- One new commit per recorded run, never amended.
- If the report cannot be committed, the verify command fails: something is wrong and must be resolved, not papered over.
- The committed file is the record. No ledger event and no database row.
- The runner keeps writing its report to the user cache, one file per run. Endless offers no option to put the report anywhere but its standardized location; such an option may come with the extracted standalone tool, not now.
- Failed reports stay in the cache, because a failed run is when they are needed for diagnosis. When a run passes, its report is MOVED (never copied) to main and the task's failed reports are deleted, so nothing is left behind once the task passes.
- One `CTRF:` line per run, naming where that run's report ended up: the committed path on a pass, the cache path on a failure.
- Always on for every project; committed reports are kept forever.

1. Runner (internal/verifycmd/report.go): write each run's report to `<user cache>/endless/verify/<id>/<UTC timestamp>.ctrf.json` instead of overwriting `ctrf.json`, so earlier failed reports survive. `printSummary` prints `CTRF: <cache path>` only when the run failed; on a pass it prints no `CTRF:` line, because the wrapper is about to move the file and will name where it lands.

2. Clean-worktree gate (src/endless/verify_cmd.py, before invoking endless-go): run `git status --porcelain` in the task's worktree and refuse when anything is listed outside the files Endless itself writes there (pending `.endless/verbs.jsonl` additions; anything gitignored is already excluded). Name the files and say to commit them, then verify again. Nothing runs.

3. Commit verb (Go, beside `CommitDoc` in internal/events/commit.go): a function that commits one named file on the project's main checkout through `commitPaths`, so it inherits main-checkout enforcement, the index.lock retry, git-env stripping, and leaving anything else that is staged alone. It must always make a new commit: give each commit a subject unique to the run (e.g. `Endless: verify E-NNNN <filename>`), and keep that subject out of AMENDABLE_COMMIT_SUBJECTS so land's orphan-strip never treats it as amendable. Expose it as an `endless-go` subcommand the Python wrapper calls.

4. Wrapper flow (src/endless/verify_cmd.py): on runner exit 0 (all passed), take the newest report in the task's cache directory (names sort by timestamp), move it into the main checkout at the path above and call the commit verb in the same step, so main is dirty only for the move-then-commit. Then delete the task's remaining cache reports (its earlier failures). Print `CTRF: <committed path>`. On a failing run do nothing: the runner already printed the cache path. If the move or commit fails, exit non-zero with a refusal naming the cause and where the report now sits, even though the suite passed, and leave the failed reports in place.

5. Docs: `.endless/tasks/CLAUDE.md` gains a third kind of file in a task's directory: verify-run reports, written by `endless task verify` and committed on main, never hand-edited or `git add`ed. Update the verify section of `endless guide orchestration` with the clean-worktree requirement, where passing reports go, and that failed reports stay in the cache until the task passes.

6. Tests:
   - Go: the commit verb makes a new commit per call (never amends, even after an earlier run's commit), refuses when the target is not the main checkout, and leaves unrelated staged files alone. The runner writes a new timestamped cache file per run without overwriting earlier ones, and prints `CTRF:` only on a failing run.
   - Python: a dirty worktree is refused before anything runs; an Endless-managed-only change is not refused; failing runs commit nothing and their reports accumulate in the cache; a passing run after them commits exactly one report at the expected path on main, leaves the task's cache directory empty, and prints exactly one `CTRF:` line naming the committed path; a commit failure makes the command exit non-zero and keeps the cache reports.

Added during implementation:
- `endless-go verify --report-dir <id>` prints the runner's per-task cache dir, so the Python wrapper asks for it instead of re-deriving `os.UserCacheDir` per platform. The cache location keeps one definition (verifycmd `reportDir`).
- The verifycmd test fixture blanks `XDG_CACHE_HOME`, so per-run reports land under the temp HOME rather than a real cache on Linux.
