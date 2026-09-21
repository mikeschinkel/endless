Implements ED-1595. Self_dev only (ED-1571): every other project has one
installed binary and no land, so none of this mechanism exists there.

# First: the premise this task was filed with is stale

This task was originally filed to invert the E-998 pin and sweep ~96 worktrees.
**Both halves are obsolete.** Measured against the live tree 2026-09-21, and
again the day before with identical results:

    worktrees with .claude/settings.json:        141
      carrying a "hooks" block:                    1   (e-2122, live session)
      carrying XDG_CONFIG_HOME:                    0
      with skip-worktree set on that file:         1

Compare the analysis on E-1972, which measured 96 of 135 pinned on 2026-08-13.
The main checkout's `.claude/settings.json` is now down to `autoMemoryEnabled`
and `enabledPlugins`. Hooks come from the user-level Claude settings file,
which points at the globally installed `endless-go`.

So **"hooks always invoke main's binary" is already true in practice**, arrived
at by the per-worktree hooks block disappearing rather than by decision. What
removed it was not determined; ED-1554's implementation (E-1964, which deletes
the XDG_CONFIG_HOME injection) and `endless worktree sync` rebases are both
candidates. **Do not build the sweep.** There is no population to sweep — one
worktree, held by a live session, which is a single case and not a migration.

# What is actually unbuilt

The other half of ED-1595. **There is currently no way to run a worktree's hook
binary at all**, so candidate hook code is never exercised and E-998's purpose
is entirely unserved. That is the gap.

1. **The declaration.** A command that marks a task as wanting its worktree's
   binary. Settable by the session or by the user, at any point in the task's
   life, changeable mid-task — discovering you are now touching hook code is
   common. Not a hand-edited file. Stored worktree-locally, not on `tasks`:
   it must be readable before `monitor.DB()` connects, a `tasks` column would be
   NULL for every project that is not Endless-developing-Endless, and the
   declaration is meaningful only while the worktree exists. Visibility in
   `task show` comes from reading that file, not from storing the value twice.

2. **The spawn.** Main's binary reads the declaration and spawns the worktree's
   binary only when it is declared AND the worktree binary's schema version
   exactly matches the database (ED-1570 — exact agreement, no compatibility
   range, in both directions).

3. **The announcement.** The session states which binary it is on in its opening
   message. This costs nothing extra — the session is already making the
   declaration — and it is what keeps the opt-in default's decay risk visible.
   The observed failure was self-concealing in both directions: a session ran a
   stale binary, then a routine `git checkout` silently moved it onto a current
   one mid-session, and neither transition printed anything.

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
- **E-2035**'s `.claude/settings.json` section is moot for the reasons measured
  above; the finding is folded into that task.
- **E-2020** owns what hooks do during the land window.
