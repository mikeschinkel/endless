Two independent halves. They share no files and can be written in either order.

## The project sweep

`endless.project_sync` — the disk-gone branch that currently runs
`DELETE FROM projects WHERE id=?` when a registered project's path is no longer
on disk. Replace it with the retention path the rest of projects already uses:
mark the row `ignored` through the same registry call `endless.unregister`'s
`unregister_project` makes, so the row, its tasks, its activity and its notes
stay and the project revives if the directory comes back. The sweep runs
automatically, so this is the half that is losing data now.

`monitor.ClearIgnoredDirectory` — guards its `DELETE FROM projects` on
`count(*) FROM tasks` being zero, which lets a project with sessions, activity,
notes or deps but no tasks through and cascades all of it away. Widen the count
to every table that references `projects`, or state in the code why tasks alone
are sufficient.

## The decisions half

A goose migration under `internal/schema/migrations/` adding
`decisions.removed INTEGER NOT NULL DEFAULT 0` and
`CREATE VIEW live_decisions AS SELECT * FROM decisions WHERE removed = 0`,
plus the same two declarations in `schema.sql` — the column last on the table,
the view beside `live_tasks` with the read rule written above it.

One `removeDecision` in `internal/events`, called from
`internal/events/decision.go`'s executor path, its second site, and
`internal/events/projector.go`'s replay path, including the legacy route where a
pre-split `task.deleted` event whose row lives in `decisions` is dispatched.
Parity is the point: a projector that still deleted while the executor marked
`removed = 1` would re-free every removed id on rebuild, on a path nobody tests.

`internal/events/executor.go`'s `SELECT COALESCE(MAX(id), 0) + 1 FROM decisions`
stays pointed at the table. Through the view `MAX(id)` would drop back and the
reuse this change exists to prevent would return while looking fixed.

Then the read audit: every list and lookup of decisions moves to
`live_decisions`; writes, revivals and the allocator stay on `decisions`.
Rows pointing at a decision that never had a foreign key — `task_deps` with
`target_kind = 'decision'` — are left in place and filtered on read through the
join, which is the half of ED-1547 that costs something: the join used to drop
them by accident because the row was gone.

The wipe at `internal/eventcmd/event.go`'s rebuild path is a scoped clear before
replay, not an entity removal, and is left alone.

## The event name

ED-1547 reserves two words: a covered entity is *removed* and its row stays, a
pure row handle is *deleted* and its row is gone. `endless task remove` already
says the right one; the event it emits, `task.deleted`, does not. Decisions have
the same mismatch, and this task is where it costs something, because it ships
new `removed = 1` behavior that would otherwise go out under a name promising
the row is gone.

So `decision.deleted` becomes `decision.removed`. `events.RetiredKinds` is the
mechanism already in the codebase for this — its comment reads "a retired one
that only replay will ever see", and it holds four kinds today. Add the new kind
to `ValidKinds`, move the old one to `RetiredKinds`, and have the projector
dispatch both into the one removal function. Nothing rewrites history: the
ledger keeps both spellings forever, and only one is ever emitted again.

`task.deleted` is the same contradiction on a table this task does not touch,
and is left to its own task.

## Not a resumption of the earlier branch

The branch's single commit is discarded and the work written fresh against main.
It was built when `internal/schema/changes/` was the migration mechanism (now
goose), when projects had no soft-delete design (now status-based, and kept
that way), and when `tests/tasks/e-NNNN-verify.sh` was the verification form
(now `.endless/tasks/<id>/verify.toml`). Eight of the files it edits have moved
or been deleted in main since.
