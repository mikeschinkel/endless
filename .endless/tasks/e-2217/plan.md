# Separate project identity from the machine-local slug

Questions answered by Mike via needs-plan, 2026-10-01.

## Decisions

1. **Identity: a project UUID generated at first registration, committed in
   `.endless/config.json`.** Same in every clone, stable across renames.
   Before implementing, check whether E-799's children already define a
   project identity and adopt that instead if they do. Backfill all existing
   registered projects.
2. **One registered checkout per project per machine.** Registering a second
   checkout whose identity is already registered is refused; the message
   points at worktrees for parallel work.
3. **Machine-local slug, separate from the committed name.** Defaults to the
   committed name; when it clashes with another registered project's slug,
   the user picks a different one at registration. It is machine-local
   state only: it names `projects/<slug>.db` (ED-1602 unchanged) and is
   never written to the ledger or to any committed file.
4. **Forks: refused for now.** A checkout whose identity is already
   registered elsewhere on the machine is refused with a message saying
   forks are not yet supported. Explicit fork support is filed separately
   (later).
5. **Ledger project stamp: decided under E-799.** The only rule set here: a
   ledger event never names its project by the machine-local slug.
6. **Enforcement: a check in `register` before the insert,** naming the
   clashing project's path and the fix (choose another slug, or use a
   worktree). The schema's UNIQUE constraints stay as a backstop.

## Work

- Add the project UUID to `config.json` and a column on `projects`;
  backfill existing projects (commit each project's `config.json` change in
  its own repo).
- Add the machine-local slug column; default it from the committed name;
  migrate existing rows (slug = name).
- `register`: refuse a duplicate identity (decisions 2 and 4) and prompt for
  a slug on clash (decision 3), with the clear refusal of decision 6.
- Point E-2215's per-project database path at the slug.

## Verification

- Registering a second clone of a registered project is refused, naming the
  existing path and suggesting a worktree.
- Registering an unrelated project whose name clashes prompts for a slug and
  succeeds with it; its database file uses that slug.
- No ledger event or committed file contains a slug that differs from the
  committed name.
