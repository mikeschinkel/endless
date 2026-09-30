Mike's 35+ years of DB experience: all tables get an id field, full stop.

Fix: SQLite migration that rebuilds the table with 'id INTEGER PRIMARY KEY' as first column (CREATE new, INSERT-SELECT from old, DROP old, RENAME, recreate index).

Existing UNIQUE(session_id, task_id) stays as a constraint.
