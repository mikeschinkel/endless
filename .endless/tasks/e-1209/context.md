because per-worktree state files in `.endless/` are intentionally untracked: `worktree.json` (companion file, never committed because committing it would rotate every worktree's contents whenever another is created/destroyed) and `worktree.lock` (ownership marker per E-971; SessionEnd hook is supposed to delete it but in practice it survives — observed on E-1186 land 2026-05-09).

Land currently surfaces this as a non-fatal error and tells the user to run `git worktree remove` manually.
