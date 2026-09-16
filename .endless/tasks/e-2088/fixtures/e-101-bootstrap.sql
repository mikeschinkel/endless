-- Fixture for E-2088's verification suite. Not a real schema change: the name
-- is in the reserved 1-199 documentation band so it can never collide with a
-- change under internal/schema/changes/, and nothing outside this suite applies
-- it.
--
-- Its only job is to be the change the INSTALLED-binary path applies, so the
-- suite can build a ledger-shaped database through `endless-go event
-- apply-change` (the connect that execs schema.sql and runs the integrity
-- gates) before the migration executable takes its turn on the same file.

CREATE TABLE IF NOT EXISTS verify_bootstrap (
    id INTEGER PRIMARY KEY
);
