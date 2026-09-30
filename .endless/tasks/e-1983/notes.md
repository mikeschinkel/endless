Post-E-1969 residual of the original marker bug.

Write-once fixed the RESUMED case — that write is now a refused reassignment — but made the FRESH-session case unrecoverable: on a session whose task_id is NULL the stale marker is the FIRST write, the trigger permits it, and `task bind` can no longer move it.

Now blocked on E-2063, which gives the window a durable Endless session id to compare against.

Settled since 2026-08-16: unsetting markers is out, no new window option is needed, and the invariant is `tmux window == Endless task == one or more Claude sessions` — in series, never concurrent.

See --analysis.
