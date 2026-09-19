Landed 2026-04-27 (c9425fb). Confirmed 2026-08-25 AS SCOPED, with the shortfall recorded here rather than by reopening.

Shipped: task and decision mutations go through the Go event-writing subcommands, the ledger is the durable record for them, and SQLite is a projection of that record.

NOT met, of this task's own stated criteria: 'all writes go through Go event-writing subcommands' and 'DB becomes a cache' hold for the task and decision domains only. Session, project and note mutations never reach the ledger, so the tables holding them are not rebuildable — owned by E-910. 'endless rebuild-db supported for disaster recovery' is currently false by design: E-2062 makes rebuild-db --confirm refuse, because its copy-back would destroy landing and gate records the ledger cannot restore.

The four tasks this unblocks (E-813, E-814, E-816, E-817) are released against the scoped result above, not against the full stated criteria.