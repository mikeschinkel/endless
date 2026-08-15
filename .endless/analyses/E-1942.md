# Analysis

## Why this is needed

`just land` takes a backup before applying a schema change, so the safety net is
already half-built. The other half does not exist: there is no way to use it.

On 2026-08-10 a land applied E-1898's migration to the real ledger and then
failed before merging, leaving the database on a schema no installed binary
understood. Session tracking froze machine-wide. Recovery was an ad-hoc `cp`,
and it went wrong twice before it went right:

1. **Copied over a live database.** Six connections were open (two
   `session-status --monitor`, four `endless task show -p` abandoned in pagers
   for up to 22 days). That left a hot `endless.db-journal`. A read-only
   connection cannot roll back a hot journal, so every reader returned
   `database is locked` and the tmux status bar flickered on and off.

2. **Journal mode silently changed.** `endless db backup` uses `VACUUM INTO`,
   which writes a **rollback-journal** database. The live DB was WAL. After the
   restore every connection fought for an exclusive lock trying to switch back.

What finally worked, none of it discoverable:

    pkill -f 'endless task show'          # abandoned pagers holding connections
    kill <session-status --monitor pids>
    sqlite3 <db> 'PRAGMA journal_mode=WAL; PRAGMA integrity_check;'

## What the verb should do

`endless db restore [<backup>]` — default to the most recent backup.

1. Refuse unless the target is a real Endless DB and the backup opens clean.
2. Enumerate open connections (the equivalent of `lsof` on the db file) and
   REPORT them by pid and command. Do not kill anything silently; either refuse
   with the list, or require an explicit flag to proceed.
3. Restore the file.
4. Re-establish `journal_mode=WAL` and run `PRAGMA integrity_check`, failing
   loudly if it is not `ok`.
5. Keep the pre-restore database aside (timestamped) so the restore itself is
   reversible.

## Notes

- Step 2 is the load-bearing one. Every failure above came from copying over a
  file other processes had open, not from the copy itself.
- A dry-run mode that prints what it would do (holders, journal mode, sizes)
  costs little and is what you actually want at 5am mid-incident.
- Related: E-1941 covers the land-flow ordering that creates the need for a
  restore in the first place. This task is the recovery tool; that one is the
  prevention. They are independent — the tool is worth having regardless.
