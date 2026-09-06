## Why this surfaced

A bulk edit needed undoing and the newest backup was nine days old. It was still
usable, but only by luck: nothing about the current design makes recency likely.

Evidence for the diagnosis, not just the symptom:

- `_backup_db` has one caller, commented "backup before any migration".
- The Go equivalent runs from the pre-apply step during land, and from an
  explicit `endless db backup`. Neither is a schedule.
- Sixty files on disk — the rotation cap — spanning three months. That is about
  one every day and a half, clustered around migrations rather than evenly.
- The newest is dated to the last schema change, which is exactly what
  backup-on-migration predicts.

The 60-second window in the docstring is a throttle so two migrations back to
back do not write two copies. It is not a cadence, and it reads like one.

## Time Machine is not the fallback

Worth recording, because it was tried first. A Time Machine snapshot of the live
database was torn — SQLite was mid-write, `PRAGMA integrity_check` failed, and
`.recover` reassembled rows wrongly: fields from one table appeared in another's
columns, and whole rows were missing. A filesystem-level copy of an open SQLite
database has no consistency guarantee.

The project's own backups are safe because they go through the SQLite backup API
and `VACUUM INTO`. That difference is the reason this job belongs in Endless
rather than being delegated to the machine's backup software.

## Shape

Follow the two jobs that already exist. A stable `JobName` (changing it orphans
the scheduling row and restarts history), a `Schedule` with an interval and a
`MaxBackoff`, registered so `session monitor` picks it up via the same RunDue
path. Unlike the model-calling jobs, this one costs no spend, so the interval is
governed by acceptable data loss, not by budget.

## Retention

Replaces keep-last-60-by-count, which is a count where it should be an age:

- hourly for 24 hours
- daily for 30 days
- weekly for a year

That is roughly 106 files at steady state. Retention must be evaluated by
timestamp tier, not list position, so a burst of migrations can no longer evict
the older tiers — the failure mode the current rotation has today.
