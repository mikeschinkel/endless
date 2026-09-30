Main's schema.sql baseline runs, so any additive change the worktree's schema declares is silently absent — leading to 'no such table' errors when subsequent ops (change files, sandbox writes) reference the not-yet-created tables.

Surfaced 2026-05-29 verifying and then landing E-1378.
