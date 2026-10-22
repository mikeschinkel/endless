# Move the session-notice fan-out from triggers into the executor

From E-2257's findings, under E-2215.

## Why

Four trigger bodies in `internal/schema/schema.sql` fire on project tables and
write the machine table `session_notices`, reading `session_tasks` and
`sessions`:

- `tasks_notify_sessions`
- `task_content_notify_insert`, `task_content_notify_update`, `task_content_notify_delete`
- `task_landings_notify_sessions`

A SQLite trigger can't reference a table in another attached database, so all
of them break when tasks move to a per-project file. This has to land before
the split.

## Work

- In the Go executor (ED-903, the single writer), compute the notice for each
  task mutation from the old and new values. Keep every rule the triggers carry:
  - which fields are watched (status, phase, complexity, risk; plan, analysis
    and notes as content; description not watched);
  - the '…' elision for content;
  - self-suppression on `changed_by_session`;
  - the harness-based suppression for landings;
  - fan-out only to sessions that aren't `ended`;
  - "one edit, one line": merge the content fields into the same notice as the
    task row change.
- Write `session_notices` after the project write commits.
- Drop the five triggers. Drop the copy in `internal/schema/migrations/00014_prime.go`
  through a new migration.
- Port the existing trigger tests to the executor path.

## Notes

- One function carries the compare-old-against-new rule the triggers existed
  to guarantee. The executor is the only writer of tasks, so no mutation site
  can skip it.
- Side benefit: a ledger replay no longer fires fan-out into its scratch DB.
