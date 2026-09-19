LESSONS.md appends from a worktree session write into the MAIN checkout, dirtying main and bypassing the branch/land flow.

## The invariant that is broken

Endless's core invariant is that a session's work happens in its own worktree at
`.endless/worktrees/e-NNN/` and `main`'s working tree stays clean. Endless auto-commits only
its own files; everything else is committed by the session on its task branch and lands via
`endless worktree land`.

`CLAUDE.md`'s "Corrections go to LESSONS.md" section instructs the opposite, explicitly:

> append the pattern to **`~/Projects/endless/.claude/LESSONS.md`** — the *main checkout*,
> always, even when you are in a worktree. Do not resolve it with
> `git rev-parse --show-toplevel`: inside a worktree that yields the worktree root, and the
> log would be destroyed when the worktree is dropped.

`.claude/LESSONS.md` is a **git-tracked file** (`git ls-files .claude/LESSONS.md` → tracked;
not gitignored). So every worktree session that records a correction writes an uncommitted
modification into main's working tree:

- `main` is no longer clean — a `git status` in main shows a modified tracked file that
  belongs to no branch and no task.
- Concurrent sessions append to the same file with no coordination; interleaved appends can
  corrupt each other.
- The change never travels through a task branch, so it is never reviewed, never landed, and
  is invisible to `endless worktree land`.
- A session working in a worktree reaches outside its worktree to mutate another checkout —
  precisely what the worktree isolation exists to prevent.

This instruction was added by a Claude Code Desktop session (commit `029208d9`, "Update for
LESSONS.md") that was not running under Endless, so it did not have the worktree invariant in
view.

## Second, related defect: two different LESSONS.md paths

`CLAUDE.md` names two different files and treats them as the same thing:

- "Corrections go to LESSONS.md" section → `~/Projects/endless/.claude/LESSONS.md` (tracked,
  166 KB)
- "Uppercase `$KEYWORD` markers" section → "do NOT hand-append a lesson to
  `~/.claude/LESSONS.md`" (a *different* file, home-level, 155 KB, exists)

Both files exist on disk with different contents. An agent reading CLAUDE.md cannot tell which
is authoritative, and the hook-driven `$JARGON` path may be writing to the other one.

## What the fix must establish

1. A worktree session recording a lesson must not dirty the main checkout.
2. The log must survive the worktree being dropped (the original rationale is sound — do not
   simply redirect the append to `git rev-parse --show-toplevel`).
3. One canonical path, named identically in every section of CLAUDE.md, and consistent with
   wherever the `$JARGON` hook actually writes.
4. Whatever mechanism is chosen should work for a real user of Endless, not only for this
   machine — including a project that is not Endless (where §3 memory governs) and a
   non-`self_dev` project.

Design work is needed to choose between the candidate approaches (an out-of-tree append target
that no checkout tracks; an `endless`-owned command that owns the write; untracking the file;
or per-worktree files reconciled at land), so this is filed for planning rather than as a spec.
