Today, 'task claim' from a CLI shell only flips task status via the event path; the session→task binding only happens via the PreToolUse hook (which fires for Claude bash calls).

Per E-1205 user report.
