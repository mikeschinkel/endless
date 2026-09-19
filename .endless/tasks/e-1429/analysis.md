## Naming consideration (2026-05-21)

Consider `--db sandbox` instead of `--db worktree`. Two readings:

- **`--db sandbox`** describes the *nature* of the data store: an isolated dev environment, throwaway. At the call site, `endless --db sandbox task add` reads as "use the isolated/test DB" to anyone who doesn't already know about the worktree-sandbox mapping. Matches common dev-tools terminology.
- **`--db worktree`** describes *location*. Matches existing internal vocabulary (CLAUDE.md uses "Worktree DB sandbox"; the cache path is `~/.cache/endless/sandboxes/worktree-e-NNN/`; the config flag is `worktree_sandbox`).

Argument for renaming: the flag UX optimizes for someone who *doesn't* already know the worktree-sandbox mapping. Argument against: terminology drift across the codebase.

If renamed, the cascading renames already have tickets:

- Path rename `~/.cache/endless/sandboxes/worktree-e-NNN/` → `~/.cache/endless/sandboxes/e-NNN/`: filed as **E-1447** (drop redundant `worktree-` prefix). Status `needs_plan`.
- Config key rename `worktree_sandbox` → `self_dev`: filed as **E-1456** (rationale: the flag describes "this project is endless self-development", not "this project has worktree sandboxing", so `self_dev` more accurately captures intent). Status `needs_plan`.

Internal vocabulary is not sacred — recommend the rename for clarity. Resolve before implementing E-1429's flag plumbing so the chosen name lands once.
