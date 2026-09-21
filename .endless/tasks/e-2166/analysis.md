Implements ED-1595. Self_dev only (ED-1571): every other project has one
installed binary and no land, so none of this mechanism exists there.

# The population, measured 2026-09-21

The per-worktree hook override lives in **`.claude/settings.local.json`**, not
`.claude/settings.json` — E-1457 (`cleans_up E-998`, landed 2026-05-24,
`16b2832`) moved it there and gitignored it. Measure the right file; an earlier
draft of this analysis measured `settings.json`, found it nearly empty, and
wrongly concluded the pin was gone.

    worktrees with .claude/settings.local.json:        138
      carrying a "hooks" block:                        115
        pinned to their OWN bin/endless-go:            115   (binary present: 115)
        pointing at the global install:                  0
      of the 115 pinned binaries, pre-E-2011 (stale):   32

Discriminator: `strings <wt>/bin/endless-go | grep -q "storing project path"`.
Compare 96 of 135 pinned on 2026-08-13 and 57 stale on 2026-08-26. The pin is
alive and roughly a third of it is stale.

# What to build

Three pieces. The first two are ED-1595; the third is what reaches the existing
population.

1. **The declaration.** A command that marks a task as wanting its worktree's
   binary. Settable by the user or by the session, at any point in the task's
   life, changeable mid-task — discovering you are now touching hook code is
   common. Neither party is privileged. Not a hand-edited file. Stored
   worktree-locally, not on `tasks`: it must be readable before `monitor.DB()`
   connects, a `tasks` column would be NULL for every project that is not
   Endless-developing-Endless, and the declaration is meaningful only while the
   worktree exists. Visibility in `task show` comes from reading that file, not
   from storing the value twice.

2. **The spawn, and the announcement.** New worktrees' settings name the
   installed binary. Main's binary reads the declaration and spawns the
   worktree's only when it is declared AND the worktree binary's schema version
   exactly matches the database (ED-1570 — exact agreement, no compatibility
   range, in both directions). The session states which binary it is on in its
   opening message: the declaration and the announcement are one act, and the
   observed failure was self-concealing in both directions — a session ran a
   stale binary, then a routine `git checkout` silently moved it onto a current
   one mid-session, and neither transition printed anything.

3. **The sweep over the 115.** Required, and it cannot ride in as a commit.
   `bin/` is gitignored, and `.claude/settings.local.json` is itself gitignored
   since E-1457 — so `endless worktree sync` (E-2090) cannot deliver it either,
   because a rebase cannot carry a file git does not track. The sweep must write
   that file directly, outside git.

   The durable form, and what makes this the last time the question is answered
   by touching 115 files: **a worktree's settings should stop naming a binary
   path at all.** Once they name the installed binary, every future decision
   about which binary runs lives inside a binary that can be updated.

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

# Do not implement the ahead-only rule

E-1972's analysis carries a superseded proposal folded in from E-2038 on
2026-08-23: markers in the DB the binary does not ship mean the DB is ahead, so
refuse, and "a binary shipping changes the DB lacks is every pre-land worktree
and must stay normal." ED-1570 was accepted two days earlier and ED-1567 says
the opposite — a candidate binary with the DB behind it refuses, because it may
never migrate the real ledger. Implement ED-1570's exact agreement.

# Related, not in scope

- **E-2134** is now the live settings-placement concern: hooks currently live
  machine-wide in the user's Claude settings, so they fire in every directory
  including projects that will never use Endless.
- **E-2035**'s `.claude/settings.json` section is written against the
  pre-E-1457 skip-worktree mechanism and should be re-read against
  `settings.local.json`. Its core — the artifact-root resolver — is unaffected,
  as is the separate generator bug it documents.
- **E-2020** owns what hooks do during the land window.
