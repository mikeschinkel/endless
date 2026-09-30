Embed internal/schema/schema.sql and execute it as a one-shot V0 bootstrap before the migrate() loop so new DBs have the foundational tables present before V1-V8 patches run.
