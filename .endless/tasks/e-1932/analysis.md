They are detectable: every one of the four tables carries a timestamp — session_tasks.created_at, session_hidden_tasks.hidden_at, session_notices.changed_at, project_next_tasks.added_at — so a row whose timestamp predates tasks.created_at of the id's current occupant provably belongs to a previous occupant.

then decide whether the check earns a permanent home in the E-1915 reconcile repair or is a one-shot migration.

Note project_next_tasks.task_id is TEXT while the others are INTEGER, so the join needs a cast.
