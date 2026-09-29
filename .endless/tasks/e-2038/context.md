The e-2011-home-relative-project-paths change file was applied to the shared main DB at 2026-08-20T18:17:55, converting projects.path from absolute to home-relative.

In self-dev, every worktree carries its OWN bin/endless-go built at its branch point, and 114 of 156 worktrees hold a binary built before that moment.

Those binaries compare a raw absolute cwd against a column that now holds ~/..., miss every rung of ProjectIDForPath's walk-up, and fall through to ensureAutoRegisteredProject — which silently registers the worktree as a project named after its directory. Zero stray rows exist before 18:17:55 and every one after it.

Nothing checks that a binary can READ the database it opens:

E-2011 is not the bug; it is the first format change to expose a standing hazard that every future one repeats.
