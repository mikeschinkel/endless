`endless db backup` exists and `just land` calls it, but there is no way to USE a backup — recovery is an unguided `cp`.

On 2026-08-10 that went wrong twice: copying over a database with six open connections left a hot journal that made every reader fail with 'database is locked', and `VACUUM INTO` backups are rollback-journal while the live DB is WAL, so connections then fought over switching it back.
