Surfaced while planning E-1929, which has to carry the import path through the removed-flag change.

'endless task import' loads a Claude plan file or JSON into the DB, with --replace bulk-clearing prior tasks from the same source file.

It looks dormant: all 14 tasks with a source_file date from April 2026 across two plan files, 13 of the 14 are declined, and task.bulk_cleared has fired 5 times in the project's entire history.

Plan attachment via 'task update --text' appears to have replaced the workflow it was built for.

Note the CLI is 'task import --json', not the 'task import-json' named in a stale code comment.
