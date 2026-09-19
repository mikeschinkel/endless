# 2026-08-26 — READ FIRST: this task is closed on purpose. Do not reopen it.

This is not an abandoned task. It was filed on 2026-08-25 without searching
first, and obsoleted eleven minutes later once its owner was found. Everything
below has been folded into **E-1972**'s analysis, under "Addendum — the land
window (evidence from E-1969, 2026-08-25)". E-1972 owns binary selection and
the compatibility gate; this task is a duplicate of that axis.

The status flip recorded no reason, which is why it reads as arbitrary. It was
not. This section exists so the next reader does not spend the investigation
again — as one did on 2026-08-26, reaching "reopen E-2061" before checking, and
having to be stopped.

If you arrived here holding new evidence about stale binaries, migrated
databases, or the land window: **append it to E-1972, not here.**

## What confirmed the closure was right (2026-08-26)

A worktree binary predating E-2011 silently auto-registered its own worktree as
a project (rows 63/64, the second such cleanup cycle). Investigating it
reproduced, independently and from scratch, analysis E-1972 already contained —
including the same absolute-vs-home-relative fingerprint argument and the same
exposed-worktree census. The area has an owner and the owner is current.

The one substantive thing this task holds that E-1972 did not was the WRITE
case — a stale binary writing against a shape it misreads corrupts rather than
logs. E-1972 covers that too, under "Failure 2 — the same binary corrupts the
SHARED ledger", with a measured incident behind it rather than a projection.

## Still open, and owed by nobody

The "false claim to retract" below is unresolved. `internal/schema/changes/
e-1929-add-tasks-removed.go:38` still asserts the window "is never entered in
practice." It is entered — E-1969's land entered it. That comment is a
standing invitation for a future change file to copy contradicted reasoning,
and closing this task did not fix it. Whoever lands E-1972 should correct it,
or it should be filed on its own.

---

# Why this is not a mis-ordering

The current step order is load-bearing in both directions, so the window cannot be closed by moving a step.

- E-1941 moved apply-change AFTER the ff-merge. Applying BEFORE left the real DB migrated to a schema no installed binary understood — unrecoverable without a restore, and on 2026-08-10 it froze session tracking machine-wide.
- It cannot move later either: _record_landing runs the same binary against the real DB, and for a branch adding a mirrored-enum value that binary carries a constant the DB lacks until the change lands (E-1664, inverted).
- Refreshing the binary earlier only relocates the window. The new binary would then meet the un-migrated DB and fail on exactly the same read.

# Why it has not bitten before

Every prior schema change was ADDITIVE. Old code does not name a column that does not exist yet, so an old binary reading a newly-migrated DB sees nothing unusual. E-1969 is the first RENAME, and a rename is the first change class where the old binary's own queries stop resolving.

The next class is worse and is not hypothetical: a change that leaves old code WRITING against a shape it misreads corrupts rather than logs, and does it silently.

# The false claim to retract

internal/schema/changes/e-1929-add-tasks-removed.go states the window 'is never entered in practice.' It is entered. internal/hookcmd/claude.go calls monitor.ReapWorktreesForProject from five places, and one of them fired during E-1969's land. Whatever lands here should correct that comment rather than leave a contradicted assertion in the file future changes copy their reasoning from.

# Candidate shapes

1. A land-in-progress marker that DB readers honor by skipping. The hook paths already fail open, so they would skip rather than error — the smallest change that makes the window safe rather than merely quiet.
2. Shrink the window: make the binary refresh part of land itself, adjacent to apply-change, instead of a Justfile step that runs after land returns.
3. Accept the window but classify it: a read caught inside it logs 'DB is mid-migration' once, not once per worktree.

Shape 1 is the only one that also covers the write case; 2 and 3 only shorten or quiet the read case.

# PRODUCT

Not an endless-developing-endless artifact. Any tracked project whose land applies schema changes has the same gap between its migrated DB and whatever binary the user's shell, editor plugin, or background agent is still holding open. On a downstream project the stale reader is likelier to be a long-lived process the user never thinks about, which makes the window longer, not shorter.
