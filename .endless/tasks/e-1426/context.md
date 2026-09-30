Companion files at .endless/sessions/claude-<uuid>.json cache fields that are either derivable on demand (cwd, pid, started_at, harness_session_id from filename) or already live in the sessions DB row (endless_session_id, pane_id-via-sessions.process).

They drift, accumulate as sad-path residue when SessionEnd fails to fire, and require their own naming, reaping, and recovery machinery — live incidents in E-1419, E-1408, E-1422 are all symptoms of this.
