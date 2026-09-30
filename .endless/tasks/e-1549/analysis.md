Replace with multi-signal guard: max(landed_at, session_tasks.updated_at) for the cutoff, plus skip-if-unmerged / skip-if-dirty / skip-if-active-session.
