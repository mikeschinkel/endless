# E-2051 — LESSONS.md follows the verbs.jsonl model

Decided with Mike 2026-08-24 in E-1733's session. The CLAUDE.md edit below is
explicitly authorized by that decision (its header otherwise forbids additions).

## Changes

1. **`src/endless/worktree_cmd.py`** — add `.endless/LESSONS.md` to
   `AUTO_COMMIT_GLOBS` (the module-level tuple near the top that currently
   carries the db-ledger and verbs globs), so land's Step 3 sweeps accumulated
   lesson appends on the main checkout into the existing
   "Endless: auto-record session activity" commit.

2. **`CLAUDE.md` — "Memory is OFF here" section** — replace the two bullets
   describing the worktree-copy convention:
   - OLD: append to `.endless/LESSONS.md` "in your own worktree — or the main
     checkout's copy if you hold no task — and commit it on your branch."
   - NEW: append to the MAIN checkout's `.endless/LESSONS.md` (never a worktree
     copy), do NOT commit — `worktree land` auto-commits it with the other
     auto-files. Keep the surrounding rules (never read it; name the full path
     you wrote to).

3. **Docs sweep** — `grep -rn "LESSONS" CLAUDE.md docs/ src/ internal/` and fix
   any other reference to the worktree-copy or commit-on-branch convention
   (the `endless guide` source included, if it mentions lessons).

## Non-changes

- No migration: main's copy already holds all current lessons (moved there
  2026-08-24; commits 63df3b38, 72e81797, 301687c9). Stale copies inside
  retained worktrees die with their worktrees.
- No new machinery: this reuses the existing auto-commit sweep unchanged.

## Verification

Create `tests/tasks/e-2051-verify.sh` (isolated, no real DB/ledger writes):

- assert `.endless/LESSONS.md` is present in `AUTO_COMMIT_GLOBS` (import the
  module and check, or grep the source);
- assert CLAUDE.md carries the new rule and no longer says "in your own
  worktree" / "commit it on your branch" for lessons;
- assert the docs sweep left no stale references
  (`grep -rn` for the old wording returns nothing).

Then set status unverified and hand off with exactly:

    esu && ./tests/tasks/e-2051-verify.sh
