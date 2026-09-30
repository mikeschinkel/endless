(1) _schema_version table with single integer, (2) one migration runner (not both Go and Python), (3) migrations run only when version is behind, (4) 'endless db migrate' command with auto-backup, (5) no table rebuilds in auto-migration.

shipping without proper migrations risks corrupting user databases.
