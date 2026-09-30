Backups only run before a schema migration: _backup_db has exactly one caller, and the Go path fires only from the pre-apply step during land and from an explicit endless db backup.

So the newest backup can be weeks old — it was nine days stale when it was needed.

The docstring's 'if last backup is > 60 seconds old' reads like a frequency but is only a throttle on a rare event.
