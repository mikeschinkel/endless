Fix (commit 351ec45): emit_event now looks up the project's registered path by name from the projects table and uses that, falling back to cwd only if the project isn't registered.

— reconciliation tracked separately as E-979.
