Implements ED-1595. Self_dev only (ED-1571): every other project has one
installed binary and no land, so none of this mechanism exists there.

# Three parts

1. **New worktrees name main's binary.** `.claude/settings.json` stops naming a
   per-worktree `bin/endless-go`. Main's binary reads a worktree-local
   declaration and spawns the worktree's binary only when it is declared AND its
   schema version exactly matches the database (ED-1570 — exact agreement, no
   compatibility range).

2. **A command that sets the declaration**, at any point in a task's life,
   settable by the session or by the user, changeable mid-task (discovering you
   are now touching hook code is common). Not a hand-edited file. The session
   states which binary it is on in its opening message, so the declaration and
   the announcement are one act — the observed failure was self-concealing in
   both directions.

3. **A sweep over the existing population.** ~96 worktrees already pin to their
   own binary. See "Blocker" below.

# Gate placement — two placements ruled out by evidence

Both from E-1972's analysis; do not re-derive.

- It must run **before** `monitor.DB()` executes `schema.SQL`. A gate after the
  connect has already let old schema land — that is how dropped triggers were
  resurrected and session writes broke machine-wide, twice.
- It must run **outside** the `pinnedToForeignRealDB()` test. `hook` calls
  `PinMainDB()` on every production invocation, and that pin makes
  `monitor.DB()` skip the schema apply *and* all five enum integrity checks. A
  gate placed where those checks live would never fire for hooks — the exact
  population it exists to catch.

# Probing the worktree binary's version

Recommended (overridable): an inert command on the worktree binary, a
`schema-version`-shaped verb that prints a version and touches no database. Its
useful property is self-bootstrapping — a binary old enough to predate the verb
fails the check by not answering, so no table of old vintages is needed.

Alternative: scan the binary file for a marker string without executing it.
Works on every vintage ever built, but breaks quietly if the marker's spelling
changes.

**Hard constraint either way (E-2071):** the probe must not itself be a hook
invocation, or it writes a row into the real database as a side effect of asking
whether it is safe to write to the real database.

# Blocker on the sweep

`endless worktree sync` exists (E-2090) and rebases task worktrees onto the
default branch. **A rebase cannot deliver a change to a skip-worktree file**, and
`.claude/settings.json` carries `git update-index --skip-worktree` in every
worktree (E-998). `bin/` is gitignored, so no commit carries a binary either.

So the existing population is reachable only if skip-worktree is lifted first, or
if the sweep writes `.claude/settings.json` directly, outside git. E-2059 folds
"settings.json / skip-worktree isolation" in as one more artifact kind; confirm
its disposition before building the sweep.

# Do not implement the ahead-only rule

E-1972's analysis carries a superseded proposal folded in from E-2038 on
2026-08-23: markers in the DB the binary does not ship mean the DB is ahead, so
refuse, and "a binary shipping changes the DB lacks is every pre-land worktree
and must stay normal." ED-1570 was accepted two days earlier and ED-1567 says
the opposite — a candidate binary with the DB behind it refuses, because it may
never migrate the real ledger. Implement ED-1570's exact agreement, in both
directions.
