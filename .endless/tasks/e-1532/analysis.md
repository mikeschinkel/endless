Apply the same FK-to-values-table pattern being introduced for task types: add task_phases and task_statuses tables with seed inserts in schema.sql (idempotent INSERT OR IGNORE), and FK columns on tasks.

Adding/removing authorized values then becomes INSERT/DELETE rather than a schema migration.
