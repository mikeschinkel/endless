# E-1734 — Block agent git ops that discard db-ledger commits — PLAN

## Mechanism
New preToolUse Bash gate `blockLedgerDiscardIfApplicable(payload)` in
`internal/hookcmd/claude.go`, called from `handlePreToolUse`'s Bash branch alongside
`blockCommitOnMainIfApplicable` / `blockSqliteAgainstEndlessIfApplicable`. It runs
only when the command matches a covered destructive verb (regex-gate FIRST, so
ordinary Bash pays no cost); on a match it resolves the commit range that verb would
discard and blocks IFF any commit in that range touched the DB WAL at
`.endless/db-ledger/`. Git is run via `exec.Command` from `payload.CWD`, modeled on
`isInMainCheckout` / `isInActiveMerge`.

## Ledger-commit detection (unifying, robust)
A range "contains a ledger commit" iff this is non-empty:
    git -C <cwd> log <range> --format=%H -- .endless/db-ledger/
Path-based, so it catches BOTH ledger commits (`Endless: record ledger entry`,
`events/commit.go` `LedgerCommitSubject`) and auto-record commits (`Endless:
auto-record session activity`) — both touch `.endless/db-ledger/` — and is robust to
subject wording. (db-ledger is the DB WAL; losing it corrupts the DB.)

## Covered verbs + range resolution
1. `git reset --hard <target>` (SURGICAL) — discarded range = `<target>..HEAD`. Parse
   `<target>` from the command. Bare `git reset --hard` (or target == HEAD) discards
   no commits -> allow. Block iff `<target>..HEAD` touches db-ledger.
2. `git branch -D <branch>` (SURGICAL) — discarded = commits reachable from `<branch>`
   but no other ref. Detect via `git log <branch> --not <all-other-refs> --format=%H
   -- .endless/db-ledger/` (build the exclude set from `git for-each-ref` minus
   `<branch>`). Block iff non-empty. (`-d` is safe-delete; git already refuses
   unmerged — still gate `-D`, and `branch --delete --force`.)
3. `git push --force` / `--force-with-lease` / `-f` (BEST-EFFORT) — discarded =
   commits on the remote-tracking ref not in the pushed tip:
   `git log <pushed-tip>..<remote>/<branch> --format=%H -- .endless/db-ledger/`.
   Uses the LOCAL remote-tracking ref, which may be stale without a fetch -> BEST
   EFFORT; the block message says so. Reliable resolution is the maybe/brainstorm.

## Out of scope (this task)
- Interactive rebase (`rebase -i`) — drop set lives in the interactive todo, not the
  command; also blocked in this harness.
- Non-interactive `rebase --onto` / `filter-branch` / other rewrites — share
  force-push's static-resolution difficulty; folded into the same maybe/brainstorm.

## Placement + block/allow discipline
- Call in the `handlePreToolUse` Bash branch, after `blockCommitOnMainIfApplicable`.
- On no verb match / empty range / any git error -> return silently (ALLOW). NEVER
  block on detection failure: a false block on a benign op trains bypass, defeating
  the gate. Surgical means block ONLY on a positive db-ledger hit.
- Block message: list the offending commit(s) (short `%h %s`), state they carry the
  DB WAL (`.endless/db-ledger/`) so discarding corrupts the DB, and give the safe
  path: `git revert` the specific bad commits, or pick a reset/branch target at or
  above the ledger commit. NO internal task IDs (user-facing-string rule). For
  force-push, add the best-effort caveat (remote-tracking ref may be stale; fetch
  first).

## New regexes (package vars)
`gitResetHardRe`, `gitBranchDeleteRe`, `gitForcePushRe` — anchored like `gitCommitRe`
(`^\s*git\s+...`), tolerant of leading whitespace and flag ordering.

## Verification
`tests/tasks/e-1734-verify.sh` (committed; shape per `tests/tasks/e-1577-verify.sh`).
In an ISOLATED temp git repo (never the real DB/ledger — see isolate-hook-verification),
seed commits mixing normal changes with a commit touching `.endless/db-ledger/seg.jsonl`:
- `reset --hard` to BELOW the ledger commit -> REFUSED; to a target ABOVE it (no
  ledger in range) -> ALLOWED; bare `git reset --hard` -> ALLOWED.
- `branch -D` of a branch whose unique commits touch db-ledger -> REFUSED; of a
  merged / no-ledger branch -> ALLOWED.
- `push --force` best-effort: with a local bare repo as "remote" whose tracking ref
  holds a ledger commit not in the pushed tip -> REFUSED.
- git error / non-git dir / unrelated Bash -> ALLOWED (no false block).
Handoff: `esu && ./tests/tasks/e-1734-verify.sh`.
