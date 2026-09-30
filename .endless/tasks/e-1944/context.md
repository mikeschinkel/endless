The 2026-08-10 incident produced four distinct failures from one question.

`monitor.DB()` applies schema.sql on EVERY connection, so any binary additively migrates whatever DB it opens; E-1818's schema-passive mode stops that for pinned surfaces but only protects the DB from the binary, never the binary from the DB — a candidate binary against a main-schema ledger (or vice versa) passes connect cleanly and then fails at runtime writing or reading columns that aren't there.

That is what took session tracking down for 30 minutes.
