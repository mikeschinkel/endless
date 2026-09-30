Fix: set PRAGMA busy_timeout on the backup connection right after sql.Open; optionally wrap with bounded retry for the case where a writer holds the lock past the timeout.
