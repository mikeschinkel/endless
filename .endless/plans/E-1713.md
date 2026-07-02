# E-1713 — Broaden canAmend to not amend a ledger commit reachable from any ref besides the current branch

## Context
Self-dev worktrees repeatedly show up as "dirty" in `endless session status` even
after a clean land, with no uncommitted changes and no real unlanded work — only a
stray `Endless: record ledger entry` commit. This wastes session time (a cold session
must re-discover that the commit is a benign duplicate) and made E-1542 look unlandable.
Root-caused during the E-1542 session; no data loss, but a recurring false signal.

## Root cause (verified)
The ledger (the DB write-ahead log under `.endless/db-ledger/`) is git-committed on every
event. The commit path (`commitPaths` in `internal/events/commit.go`) calls `canAmend` to
decide whether to `git commit --amend` main's tip ledger commit or append a new one.
`canAmend` amends only when: HEAD's subject is the ledger subject, the index is clean
outside the ledger glob, and **HEAD is not reachable from any `origin/*` ref** — i.e. a
"don't rewrite already-published history" guard. That guard inspects **remote refs only**.

`ensureMainCheckout` (same file) guarantees the commit always lands on the main checkout,
never a worktree branch — so a worktree branch only ever *inherits* main's ledger tip, via
the rebase in `land_worktree` (`src/endless/worktree_cmd.py`). Sequence of the bug:

1. `land` rebases the worktree branch onto main's current tip — publishing that tip
   (a ledger commit) to the worktree branch.
2. The next ledger event on main hits `canAmend`, which sees an unpushed ledger tip and
   **amends it in place** — rewriting its SHA while keeping the same parent.
3. The worktree branch still points at the pre-amend SHA. It is now an orphan of an
   amended commit: same parent as main's tip, content a strict subset of main's.
4. `taskWorktreeDirty` (`internal/monitor/reap_worktrees.go`) counts `main..HEAD` > 0 and
   reports the worktree "1 ahead"/dirty. Re-landing re-syncs, then the next main-side event
   re-amends and re-orphans — so it never clears.

Live evidence at diagnosis: the worktree tip and main tip shared the same parent and the
same ledger segment file, with main a strict superset (one extra line) — one commit main
amended after the worktree forked. All entries confirmed present on main (no loss).

Analogy: it is the classic git rule "never amend a commit you've already published." The
code already enforces it for the *remote* publish (`origin/*`); it fails to enforce it for
the *local* publish — a worktree branch that landed onto main's tip.

## Fix
Generalize `canAmend`'s published-history guard from "not reachable from `origin/*`" to
"not reachable from **any ref besides the current branch**." One inspection covers remote,
local, and worktree refs: `git for-each-ref --contains HEAD` over all refs, excluding the
current branch (resolve the current branch via `git symbolic-ref --short HEAD` /
`git branch --show-current`). If any *other* ref contains HEAD, refuse to amend and append
a new commit instead.

Effect: whenever a worktree branch (or a remote) is based on main's ledger tip, main
appends rather than amends, so the worktree's tip stays an **ancestor** of main
(`main..HEAD == 0`) — no orphan, no false-dirty, no land-churn. The amend optimization
still applies in the common case where nothing else references the tip, keeping ledger
history compact there.

## Alternatives considered & rejected
- **Teach `taskWorktreeDirty` to ignore ledger-amend orphans** — a detector bandaid; the
  stale branch still exists and every other `main..HEAD` consumer still trips on it.
- **Have `land` reset the worktree branch to main's tip** — does not durably hold: main
  keeps amending its tip between lands, re-orphaning retained worktrees seconds later.
- **Change ledger commit frequency (batch/defer instead of per-event)** — trades away the
  per-event WAL durability guarantee (a crash between writing the JSONL line and committing
  it would lose an entry) and is orthogonal to the amend-vs-shared-ref problem.

## Automated tests (authored here, run by the verify script)
- **Unit around `canAmend`** (fixture git repo), in `internal/events/` (e.g. `commit_test.go`):
  (a) a *non-current* local branch contains HEAD, HEAD's subject is the ledger subject, HEAD
  is unpushed → `canAmend` returns false (append); (b) no other ref contains HEAD → returns
  true (amend); (c) the existing `origin/*` refusal still holds; (d) current-branch exclusion
  — main itself containing HEAD must NOT block the amend.

## Verification
The **only** thing handed to Mike after implementation:

    esu && ./tests/tasks/e-1713-verify.sh

Per E-1596's convention, `tests/tasks/e-1713-verify.sh` is a first-class deliverable of this
task — an ad-hoc, committed, per-task script whose shape/output is modeled on the
`tests/tasks/e-1577-verify.sh` prototype (a reference to learn from, not a dependency; the
shared harness E-1596 is building will subsume it later). It is self-contained and is the
entire verify handoff:
1. Ensures binaries are current (builds if `bin/` is stale).
2. Runs the Go unit tests above (`go test ./internal/events/...`).
3. Behavioral E2E in a throwaway fixture: create a task worktree, land it (its branch now
   sits on main's ledger tip), emit another ledger event on main, then assert the worktree
   branch tip is STILL an ancestor of main (`git merge-base --is-ancestor <worktree-tip>
   main`) and that `taskWorktreeDirty` / `endless session status` does NOT flag it dirty.
   Fails on baseline (bug present), passes with the fix.
4. Prints **`ALL PASSED`** (exit 0) or a numbered, diagnosable failure list (exit 1) — the
   E-1596 result contract.

No manual verification step is expected; the one command above is the complete handoff.

## Critical code sites (by symbol)
- `canAmend` and `commitPaths` in `internal/events/commit.go` — broaden the shared-ref
  inspection (the core change).
- `ensureMainCheckout` in the same file — context: why worktree branches only ever inherit
  main's ledger commits (no change).
- `taskWorktreeDirty` in `internal/monitor/reap_worktrees.go` — the observable false-dirty
  surface to verify against (no change expected once the amend is fixed).
- `_drop_orphan_amendable_commits` / `land_worktree` in `src/endless/worktree_cmd.py` —
  context on why base-orphan stripping doesn't durably help (no change).

## Out of scope
- The dirty-check bandaid and the land-reset approach (see rejected).
- Any ledger-commit-frequency redesign.
