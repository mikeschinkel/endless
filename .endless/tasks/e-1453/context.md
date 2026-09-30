cmd/endless-event main.go calls monitor.DB() to get the connection;

DB() unconditionally invokes migrate(db, MigrateOpts{Runner: RunnerAuto}) before main.go reads -dry-run.

So the real migration runs, then the dry-run code path reports everything 'already applied' — misleading no-op.

Discovered during E-1396 when a -dry-run command actually applied V11 to the real DB.
