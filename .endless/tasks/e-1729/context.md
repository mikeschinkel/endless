emit_event resolves project_root from the projects table (the real checkout) and appends+commits event JSONL to the real .endless/db-ledger/ regardless of --db sandbox.

So every sandbox operation — e.g. every 'endless task add --db sandbox' in a tests/tasks/*-verify.sh — permanently writes test-task events (ids 1,2,3... under per-sandbox node ids) into the PRODUCTION ledger, colliding with real E-1..E-96 and making validate-db/rebuild-db noisy.

Surfaced by E-1719's validate-db check.
