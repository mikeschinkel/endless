Migration: drop+recreate each table preserving data, change column to 'id INTEGER PRIMARY KEY AUTOINCREMENT', seed sqlite_sequence with the current max(id).

Tables to audit: tasks, sessions, projects, focuses, channels, conversations, messages, notes, task_deps, task_files, session_messages, session_tasks, session_statuses, session_gates, suggestions, activity.

E-1344 (soft delete) addresses the same concern via a different lever; both belong long-term — AUTOINCREMENT is belt-and-suspenders for any future hard-purge path.
