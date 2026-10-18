# Recover main from a migration it should not have taken

A user-only command (like `db restore`), e.g. `endless db recover`.

1. Detect: main's schema version is ahead of the installed endless-go
   (the ED-1570 condition). Say so, and name both versions.
2. Pick the backup the migration took just before it applied, by its link to
   that migration, never "newest", so a later scheduled backup cannot
   displace it.
3. Maintenance lock (the core): a lock every Endless opener (CLI, hooks,
   tmux status line, monitors, jobs) checks and honors by refusing quietly
   while it is set. The command sets it, waits for open handles to close
   (naming any that remain), and clears it on every exit path.
4. Atomic restore: copy the backup to a temp file beside the database and
   rename it into place, so the database path is never empty. `db restore`
   uses the same path, which closes its race on its own.
5. Verify: schema version, PRAGMA integrity_check, and row counts against
   the backup.
6. Offer the alternative: when the branch that migrated is landable, say that
   landing it (roll forward) is the cheaper fix, and name the command.
7. Later child (optional): rebuild task state from the ledger, so task
   changes made after the backup come back. Machine-local state does not.

Tests: an opener during the restore window is refused rather than creating
a database; the lock clears after a failed restore; the chosen backup is the
pre-migration one even when a newer backup exists.
