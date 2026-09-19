# E-1308 — Detect already-landed situations in `worktree land` and message accordingly

## Context

`endless worktree land E-NNN` mishandles three "the work already landed" situations
by treating them as failures:

- The worktree is gone → `_branch_for_task` returns `None` and land raises
  `"No endless-managed worktree for E-NNN"` (`worktree_cmd.py:1628-1632`), implying
  the work is lost. It isn't — it's in main. NOTE the real mechanism (land does NOT
  remove worktrees): the reaper removes the dir AND branch (`git worktree remove` +
  `git branch -D`, `reap_worktrees.go:217,237`) once the task's `task_landings` row
  is older than `worktree_ttl` (default 14 days), or a human ran `git worktree
  remove` / `endless worktree drop`. So this is a LATER condition, not an
  immediate-after-land one; an immediate multi-session re-land still finds the
  worktree present and re-lands idempotently.
- The branch was rewritten relative to a landing that already happened (e.g. a
  `git commit --amend` of an already-landed commit) → land proceeds straight into a
  confusing Step-4 rebase conflict. Observed repro: the E-1750 land amended a
  landed commit; the re-land conflicted in `session_status.go`.

Full scope decided with Mike 2026-07-11: cover all three via one shared last-landing
lookup — A1 (branch merged), A2 (branch also reaped), B (branch is a rewrite).

## 1. New capability — read a task's last landing (Go)

No Go query returns a task's landing SHA today (`session-query` has none; the Python
`landed_item` path reads `task_landings` via SQLite, which new code must not extend —
E-1486). Add subcommand **`session-query last-landing --id <task-id>`** in
`internal/sessionquerycmd/session_query.go` (dispatched from the `session-query` case
in `cmd/endless-go/main.go`; update the usage lines at `main.go:14` and `:195`).

Query (main DB):

    SELECT merge_commit_sha, branch, landed_at
      FROM task_landings WHERE task_id = ?
     ORDER BY landed_at DESC, id DESC LIMIT 1

Output: JSON `{"merge_commit_sha","branch","landed_at"}`; empty output when the task
has no landing (mirror the empty-handling of the existing subcommands, e.g.
`task-text`). Python wraps it as `_last_landing(task_id) -> dict | None` via
`shutil.which("endless-go")` + `config.go_db_context_args()` (land already pins main
through `default_db_to_main`), returning `None` when there is no landing.

## 2. Facet A — worktree already gone (`land_worktree`, the `target is None` branch ~L1632)

Reached only when the worktree dir is gone (reaper past `worktree_ttl`, or a manual
removal). Before raising "No endless-managed worktree", in order:

- **A1 (git-only):** a branch matching `task/<NNN>-*` exists AND is listed by
  `git branch --merged <base>` → print
  `"E-<NNN> already landed (<branch-tip-short-sha>); its worktree directory is gone
  but the branch is merged. Nothing to do."` and RETURN success (exit 0). (This is
  the narrower case where the dir was removed but the branch survived — e.g. a bare
  `git worktree remove` without `branch -D`.)
- **A2 (uses §1):** else, if `_last_landing(NNN)` is not None → print
  `"E-<NNN> already landed (<merge_commit_sha-short>); its worktree and branch were
  reaped. Nothing to do."` and RETURN success. (The common post-reap state — the
  reaper deletes the branch too — so A2, not A1, is what a normal aged land hits.)
- **A3:** else → today's `"No endless-managed worktree for E-<NNN>"` error (the work
  is genuinely unaccounted for).

The subject task's own `E-<NNN>` may appear (the user typed it); the house-rule ban is
on citing UNRELATED internal task IDs — none here.

## 3. Facet B — branch is a rewrite of already-landed content (pre-rebase gate)

Insert after Step 3.8's dirty guard (`_guard_dirty_worktree`, ~L1741) and BEFORE
Step 4's `git rebase base_branch` (~L1743):

- `land = _last_landing(NNN)`; if `None` → skip (first land — normal).
- Else if `git -C <wt> merge-base --is-ancestor <land.merge_commit_sha> HEAD` exits
  non-zero → HEAD is NOT a descendant of the landing → the branch was rewritten
  relative to it (e.g. an amended already-landed commit). **Refuse before the rebase
  mutates anything:** `raise click.ClickException(msg)`. Because the descendant check
  is DEFINITIVE (unlike E-1417's Step-4 handler, which guesses among causes), `msg`
  names the cause confidently: HEAD is not a descendant of the branch's recorded
  landing at `<sha>`, so land is refusing to avoid duplicating already-landed work.
  It then presents the SAME already-landed recovery E-1417 ships — the shared
  `_already_landed_recovery(wt, base)` snippet (delta captured to a patch FIRST, then
  reset + re-apply), so the sequence never loses the branch's new work even when the
  runner didn't author it — plus a one-line note to confirm branch ownership / that
  the captured delta is the intended change before running it, and inspection hints
  (`git -C <wt> log <base>..HEAD`, `git -C <wt> diff <base>...HEAD`). No emojis, no
  unrelated internal IDs.
- Else (descendant, or no landing) → proceed to Step 4 unchanged.

A correct follow-up (a new commit on a branch first rebased onto main) passes,
because main contains the landing SHA; only an amend/rewrite of the landed commit is
refused. This makes the amend case a precise PRE-conflict refusal; E-1417 remains the
after-conflict safety net for source conflicts B cannot predetermine.

## Files
- `internal/sessionquerycmd/session_query.go` (+ usage lines in `cmd/endless-go/main.go`)
  — the `last-landing` subcommand.
- `src/endless/worktree_cmd.py` — `_last_landing` helper; Facet A block at the
  `target is None` branch; Facet B gate before Step 4; extract
  `_already_landed_recovery(wt, base)` from the shipped `_rebase_conflict_message`
  candidate 2 and call it from both that function and Facet B (keeps the wording
  identical; a pure refactor of E-1417's landed code, behavior unchanged).

## Coordination with E-1417 (LANDED — commit b1efcc28)
E-1417 already shipped `_rebase_conflict_message` in `worktree_cmd.py`. Its
source-conflict "candidate 2" is the already-landed recovery and is SAFE by
construction — it captures the branch's delta to a patch BEFORE resetting:

    git -C <wt> diff <base>...HEAD > /tmp/land-delta.patch
    git -C <wt> reset --hard <base>
    git -C <wt> apply /tmp/land-delta.patch
    git -C <wt> commit -am "<describe your change>"

E-1308 branches from current main (which has E-1417) — no rebase-onto-in-flight.
To keep B's refusal and E-1417's candidate byte-identical, EXTRACT that recovery
block into a small shared builder (e.g. `_already_landed_recovery(wt, base) -> str`)
and call it from BOTH `_rebase_conflict_message`'s candidate 2 and Facet B's message.
`_rebase_conflict_message` itself is only reachable mid-conflict (it reads
REBASE_HEAD / unmerged paths), so B cannot call it directly — B builds its own
message from the shared recovery snippet plus the definitive cause line. No
behavioral clash: B refuses earlier, so E-1417's Step-4 handler is simply not reached
for the rewrite case.

## Tests
- Go (`internal/sessionquerycmd`, pattern per its `*_test.go`): `last-landing` returns
  the newest landing's SHA; empty when none.
- Python worktree-land suite (real git repos, pattern per
  `tests/test_worktree_land_orphan_drop.py`):
  - A1: worktree dir removed + branch merged → friendly message, exit 0.
  - A2: worktree + branch gone + a `task_landings` row → friendly message.
  - A3: neither → today's error preserved.
  - B: landed once then branch amended (not a descendant) → refuse before rebase with
    reset+reapply; branch with a new commit stacked (descendant) → proceeds.

## Verify
Deliver `tests/tasks/e-1308-verify.sh`; handoff is exactly:

    esu && ./tests/tasks/e-1308-verify.sh

Exercises A1/A2/A3/B against crafted throwaway git repos plus the `last-landing` Go
query, per the model of `tests/tasks/e-1577-verify.sh`.

## Non-goals
- Auto-recovering the rewrite (Facet B refuses + instructs; it does not reset for the
  user — land stays non-interactive).
- Changing the reaper or the landing-record flow.
- Replacing E-1417's after-conflict diagnosis (kept as the safety net).
