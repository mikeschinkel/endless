Decided in the E-1883 brainstorm (Mike, 2026-10-01).

Today one machine-wide SQLite file holds every registered project's tasks,
decisions and events, while each project's db-ledger lives in its own repo.
Rebuilding from one project's ledger therefore loses every other project's
rows. A surgical in-place rebuild was rejected: it needs a hand-maintained
classification of every table and column as project- or machine-scoped, which
invites corruption when it is wrong, and rebuilding every project on each
rebuild is too slow.

Endless is about to manage projects other than itself (go-cfgstore, gomion).
