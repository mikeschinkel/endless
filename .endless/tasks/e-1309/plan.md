# E-1309: Route ledger auto-commits to project main checkout, not caller cwd

## Problem

`endless worktree land` for E-1329 (2026-05-14) failed on rebase because a commit titled `"Endless: record ledger entry"` had accumulated on the task branch instead of on `main`. Same failure mode as the one that produced this task on 2026-05-13. The rogue commit grouped events from two concurrent sibling Claude sessions (349 in main checkout, 389 in worktree e-1329) and landed on the worktree's task branch even though the JSONL file modification went into main's working tree.

Diagnostic captured at `/tmp/e-1309-e-1329.md` (transient).

## Root cause

`internal/events/commit.go::runGit` builds `exec.Command("git", ...)` without setting `cmd.Env`, so the subprocess inherits the parent's environment unchanged. When the caller's shell has any of `GIT_DIR`, `GIT_WORK_TREE`, `GIT_INDEX_FILE`, `GIT_OBJECT_DIRECTORY`, `GIT_COMMON_DIR`, `GIT_NAMESPACE`, or `GIT_ALTERNATE_OBJECT_DIRECTORIES` exported, those variables take precedence over `git -C <projectRoot>` for repository resolution. Result: the file is written to main's working tree (`--project-root` is honored at the file-IO layer), but `git add` and `git commit` operate on the worktree's `.git` and update the task branch's HEAD.

Evidence from the diagnostic:

- `event_bridge.py` (post-E-972 fix) is loaded from the main checkout's editable install.
- `projects.path` correctly points at the main checkout.
- The JSONL file ended up at `/Users/mikeschinkel/Projects/endless/.endless/db-ledger/db-entries-a7f3-000003.jsonl` (main's tree).
- The commit `51b85e0` containing five appended events landed on `task/1329-...`, not on `main`.
- Worktree git-dir vs git-common-dir differ; main's match.

The same vulnerability exists in Python `commit_project_verb` (`src/endless/matchers.py:913-950`), which uses `subprocess.run(["git", ...])` with inherited env.

## Fix

### 1. Sanitize git env in `internal/events/commit.go::runGit` (and `runGitOutput`)

Build `cmd.Env` from `os.Environ()` with the following variables stripped before invoking git:

- `GIT_DIR`
- `GIT_WORK_TREE`
- `GIT_INDEX_FILE`
- `GIT_OBJECT_DIRECTORY`
- `GIT_COMMON_DIR`
- `GIT_NAMESPACE`
- `GIT_ALTERNATE_OBJECT_DIRECTORIES`

After this, `git -C <projectRoot>` becomes authoritative. Fixes both `CommitLedgerSegment` and `CommitSnapshotPair` since they share `runGit`.

### 2. Add a worktree-detection guard to `commitPaths`

Before staging, run `git -C projectRoot rev-parse --git-dir --git-common-dir` with the sanitized env. If the two values differ, `projectRoot` resolved into a linked worktree somehow (projects-table corruption, bad caller). Error loudly with a message naming the projectRoot and both git-dir values. Belt to the suspenders of #1; catches any future regression where a worktree path leaks into projectRoot.

### 3. Mirror the env fix in Python `commit_project_verb` (`src/endless/matchers.py`)

`commit_project_verb` shells to git via `subprocess.run`. Pass an explicit `env=` with the same `GIT_*` variables stripped. Same vulnerability, same fix.

### 4. Tests

- **Go unit:** in a test that sets `GIT_DIR=<bogus-path>` in the test process env, call `runGit(<real-repo>, "rev-parse", "--git-dir")` and assert the result is `<real-repo>/.git`, not the bogus path.
- **Go integration:** create a temp repo, `git worktree add` a linked worktree, set `GIT_DIR=<worktree-gitdir>` in env, call `CommitLedgerSegment(<main-root>, "<file>")`, assert the commit landed on main's branch (`git -C <main> log -1 --format=%H`) and not on the worktree's branch.
- **Go unit (guard):** call `commitPaths` with `projectRoot = <a worktree path>` and assert it returns the worktree-detection error without committing.
- **Python:** parallel unit test for `commit_project_verb`.

### 5. Verification

After fix lands, retry the E-1329 land (after dropping its rogue commit per `/tmp/e-1329-fix.md`) and confirm `git log main..HEAD --oneline` on the task branch shows only the feature commit, with no `"Endless: record ledger entry"` entries.

## Loose ends / follow-ups

- **Investigate where `GIT_DIR` enters the worktree shell.** Not blocking the fix (since the fix is robust regardless of source), but worth knowing. Candidates: `esu`/`esp` shell helpers, tmux env, a wrapper around `git worktree add`, or some Claude/IDE integration. File as a separate task (`--cleans-up E-1309`) once curiosity demands; the fix doesn't depend on the answer.
- **E-1329 worktree cleanup.** The rogue commit `51b85e0` must come off the task branch before E-1329 can land. Instructions at `/tmp/e-1329-fix.md`. Track as part of E-1329's verify cycle.
- **`emit_event` cwd fallback.** `event_bridge.emit_event` falls back to `Path.cwd()` if the project isn't in the projects table. Defensible but masks bugs. File a follow-up to remove it; not load-bearing for this fix.
- **E-1281 sandbox interaction.** When sandbox lands, ledger writes won't touch main at all, so this whole routing question becomes moot for sandboxed sessions. The env-sanitization in #1 is still correct in that world; the guard in #2 may need to allow the sandbox path. Leave a code comment noting this.

## Out of scope

- Centralizing main-checkout lookup helpers (Python or Go). The existing resolution is correct; the bug isn't there.
- E-1085 cross-worktree dep handling.
- Implementing the sandbox bypass for E-1281.

## Files touched (estimate)

- `internal/events/commit.go` — env sanitization in `runGit`/`runGitOutput`; new worktree guard in `commitPaths`.
- `internal/events/commit_test.go` (new or extended) — three Go tests.
- `src/endless/matchers.py` — `env=` arg on `subprocess.run` in `commit_project_verb`.
- One new Python test alongside.

Tight blast radius. No schema changes, no API changes, no migration.
