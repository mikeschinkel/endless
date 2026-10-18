On 2026-10-18, E-2217's worktree (branch adding migration 22) ran the full
`just test` and `just test-go` suites in parallel. At 04:16:50Z, inside that
run, the MAIN database (~/.config/endless/endless.db) was backed up and
migrated to 22, while the installed endless-go was still at 21. Every other
session then failed with "the binary is older than the database" (ED-1570),
and Mike had to restore from backup (`db restore`), losing about 45 minutes of
writes.

ED-1601 says a worktree build never opens main. The guard
(internal/monitor/db.go `candidateBuild`) decides "worktree build" ONLY from
whether the executable's path contains the `.endless/worktrees` segment. Branch code
running from any other path is unguarded:
- the endless-go that tests/conftest.py builds into the system temp dir (E-1921);
- `go test` binaries, which run from the Go build cache;
- a branch binary copied out of the worktree (the E-2280 suite pattern does
  this deliberately, under the verify runner's temp HOME).

Any of these, run without a temp HOME, resolves main and auto-migrates it.
Not yet pinned: WHICH test, in which suite, opened the real main database.
