# E-2000 — implementation notes

Branch is a **single commit on top of current main**; land needs no rebase.

## Original defect

`CLAUDE.md` told every session — explicitly including one inside a worktree — to
append corrections to the main checkout's tracked `.claude/LESSONS.md`, leaving
an uncommitted change in a checkout the session was not working in. The rule and
its rationale both arrived in `029208d9`, a Claude Code Desktop session not
running under Endless. `git show 029208d9 -- CLAUDE.md` confirms there was no
earlier "always main" position.

## What main did independently

While this task was underway, main moved the log itself (`b0657be7`,
`459a980a`, `035d6e6f`) and edited CLAUDE.md (`61e14483`, `895edfe5`). So the
`git mv` this branch had done was superseded. The branch was reset to main and
only the remaining delta re-applied; superseded history is at
`backup/e-2000-preresolve`.

## What this commit contributes

**1. A coherent rule in CLAUDE.md.** Main's copy had absorbed the new path but
kept the old sentence around it — "append to `<worktree>/.endless/LESSONS.md` —
the *main checkout*, always, even when you are in a worktree" — plus the
worktree-drop rationale. Both halves cannot be followed. Replaced with: append
to your own worktree's copy, commit on your branch, it reaches main when you
land; a session in main with no claimed task appends there. Adds why the file
lives in `.endless/` (Endless artifact, `self_dev`-only) rather than `.claude/`
(the harness's directory).

The worktree-drop rationale is deleted, not softened: land retains the worktree
and its branch, and the reaper only sweeps after the work is on main.

**2. `merge=union` for `.endless/LESSONS.md`** — the corrections log, and
nothing else; not the DB ledger, not `.endless/logs/` — beside the `.endless/verbs.jsonl` line present
since E-1268. Merge attributes are per-path, so this is scoped to the log alone.

Measured from pristine fixtures, with a control:

| case | result |
|---|---|
| two branches append different entries | clean, no duplication |
| identical entry on both branches | clean, kept once (one change, not a conflict) |
| branch edits header, other appends | clean, both preserved |
| both branches edit the SAME line | clean, **both lines survive** |

Only the last is a hazard, and it is the one union is known for.

Difference from verbs.jsonl worth knowing: that file has a load-time dedup
reader (`matchers._add_verb_to_file`) to absorb union's duplicates. This file's
only reader is a human, so there is no dedup pass — hence the same-line case,
not appends, is the one to know about.

Not extended to `.endless/db-ledger/*.jsonl`, where concatenating both sides
would duplicate DB mutation records; that directory avoids conflicts by sharding
filenames per machine. The suite asserts its merge attr stays unspecified.

The rule's comment is asserted for shape, not just effect. The first version of
this commit re-applied the block with `tail -16` on an 18-line comment, clipping
its opening two lines so it began mid-sentence and no longer named the file it
governed — while `check-attr` reported `union` throughout, so the attribute test
passed over a shredded comment. Three assertions now cover the artifact itself.

**3. The log's header** still said "Review this file at the start of each
session" — the opposite of the write-only rule. Rewritten to agree with
CLAUDE.md; this session's nine entries appended.

## Rejected: a mechanism (built, then removed)

An earlier revision shipped per-task `.claude/lessons/E-NNNN.md` files and a
`_fold_task_lessons` step in `land_worktree`. Removed as over-engineering for a
`self_dev`-only convention, and because the measurement justifying it was false:
a fixture that rebuilt branches from a main which had already absorbed one of
them, so it appended the same entry twice and "proved" union duplicates. The
verify suite had pinned that false claim and passed. Both the claim and the
mechanism are gone; the corrected measurements are in the suite.

**This commit carries no product code** — `src/`, `internal/`, `cmd/` untouched,
asserted by the suite.

## Files

- `CLAUDE.md` — the corrections rule.
- `.gitattributes` — `merge=union` for the log, with the DB-ledger carve-out.
- `.endless/LESSONS.md` — header rewritten; nine entries appended.
- `tests/tasks/e-2000-verify.sh` — 41 checks.
