## The precedent to copy

`internal/sessionstatuscmd/session_status.go` builds the footer:

    return fmt.Sprintf("… %d hidden (--show-hidden)", n)

E-1914 established the rule behind it — the view "never hides silently" — and
that is exactly the property the listing surfaces lack. This task applies the
existing idiom to result-set truncation rather than inventing one.

## Why more flags would not have fixed it

`task search` already has `--limit` (E-864), and it did not help. The cap that
bit was in the CALLER's pipe, not in the command, because the caller could not
predict the result size and so bounded it defensively. A cap that the tool owns
and announces removes the reason to reach for `head` at all; a flag the caller
must remember to pass does not.

## Recommended shape, for the implementor to confirm

- A default row cap on human-readable renders, with a footer naming what was
  dropped and how to see it, e.g. "… 47 more rows (--limit 0)".
- `--limit 0` (or `--all`) means no cap. Whatever spelling the surrounding
  commands already use wins over inventing a new one.
- MACHINE-READABLE formats are the trap. Silently capping a `--json`, `--llm` or
  `--tsv` payload would be strictly worse than today's problem: a consumer
  parsing 50 of 200 rows has no footer to read and no way to notice. Either
  leave those uncapped, or carry the truncation in the payload itself
  (`"truncated": true, "total": N`) so it is impossible to miss. Do not emit a
  human footer into a machine format.
- `endless sql` is the raw escape hatch and the one I actually truncate most.
  It deserves the cap even though — maybe because — it is the least structured
  surface.

## Evidence, from the session that filed this

Two false negatives, same mechanism, hours apart:

- Searching for an owning epic before filing rebuild work: `head -30`, ordered
  by id ASC, hid E-1935 (an epic that already owned the area). Three tasks were
  filed under the wrong parent as a result and had to be moved.
- Searching for existing work on a pre-completed status: `head -24`, ordered by
  id ASC, hid E-1535 and E-1663. A design question was reported to the user as
  settled when an existing task contradicted it.

Both were ordered ascending, so truncation removed the newest rows — and
recency is the relevance signal here, since recently filed tasks get attention
that months-old ones do not.



---

## CORRECTION (2026-08-28): the pattern already exists — copy it, do not invent it

This task was filed saying the listing surfaces have no cap. That is true of five
of them and FALSE of a sixth, which already does exactly the right thing:

    endless task unsettled --all

    … 64 more rows (--no-limit)

    --limit INTEGER    Max rows to render (default: 20; --no-limit for all).
    --no-limit         Render every row, however many there are.

So the default cap, the footer, and the escape flag are all already built and
shipped there. Measured 2026-08-28: `task unsettled` has them; `task list`,
`task recent`, `task active`, `task next` and `task search` have no limit flag at
all.

Two consequences for whoever picks this up:

1. **The spelling is already decided.** My recommendation above suggested
   `--limit 0` or `--all` for "no cap" — IGNORE THAT. `task unsettled` uses
   `--limit N` plus a separate `--no-limit`, and a second spelling for the same
   idea would be worse than the inconsistency this task exists to fix. Follow the
   existing one.
2. **The work is propagation, not design.** Lift whatever renders that footer out
   of the unsettled path and apply it to the surfaces that lack it. That also
   makes the machine-format question above easier to settle: check what
   `task unsettled` already does for `--json` and match it, or if it does nothing
   there, that gap is the only genuinely new design in this task.

Note `--all` on `task unsettled` means "survey every worktree" (which rows to
consider), NOT "show every row" (how many to render). Two different axes that
read alike; do not collapse them.



## Flag spelling, settled 2026-08-28 by Mike

Ship BOTH spellings as synonyms:

    --limit 0      terse, for agents
    --no-limit     legible, for humans

`--limit 0` alone was judged cryptic; `--no-limit` alone loses the terse form.
They are the same switch, so implement one and alias the other rather than
carrying two code paths.

`--all` is NOT in play and must not be recruited for this. It already means a
different axis on the surfaces that have it — WHICH ROWS to consider, not HOW
MANY to render: `task list --all` includes terminal statuses, `task unsettled
--all` surveys every worktree rather than one. Overloading it to also mean "no
cap" would collide on exactly the commands that need both at once.
