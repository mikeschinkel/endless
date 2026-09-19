# Milestone

Two developers collaborate on one Endless-tracked project with no shared live database — all shared state travels through git.

## Must work end to end

1. Dev A and Dev B both file tasks and decisions locally.
2. Each commits the db-ledger and pushes; each pulls the other's ledger.
3. Each rebuilds the SQLite projection from the merged db-ledger with no FK, UNIQUE, or ordering failures.
4. No identifier collisions between independently-authored tasks (resolved by the sibling ID-scheme brainstorm).

## Related robustness work (already filed)

- E-1041 — rebuild-db FK and UNIQUE failures from pre-event-sourcing data.
- E-1671, E-1672 — event-upcasting pipeline and forward-only upcasting transform.
- E-1675, E-1676 — upcasting coverage linter and rebuild trigger on land or pull that adds a migration.
- E-1716 — neutralize committed test-fixture ledger events.

These are the rebuild-side dependencies; the ID scheme is the other half.