db-ledger is PROJECT-scoped (lives at <project>/.endless/db-ledger, designed so the DB can be rebuilt from it), but ~/.config/endless/endless.db is MACHINE-global across ALL registered projects.

Raised by Mike 2026-08-04 during E-698.



## Second driver (Mike, 2026-10-01)

Endless is about to manage projects other than itself for the first time
(go-cfgstore, gomion), so this question moved up. Mike's reason for a split is
different from rebuild safety: what belongs to a project should stay with that
project, in that project's Git repo. Today every project's state lives in one
machine-global database outside any repo.

The brainstorm must also judge whether a split is NEEDED at all, not only how
to do one. The ledger may already give the repo what it needs, which would make
option 5 (or 2) enough. Weigh both drivers — rebuild safety and project-owned
state — against the cost of options 1 and 4.
