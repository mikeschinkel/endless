Reopening an assumed/completed task currently requires 'endless task claim --force', which binds the task to the operating session and blocks handoff via 'endless task spawn'.

Surfaced on 2026-06-11 during E-1434 cleanup-then-spawn flow: orchestrator 'claim --force' blocked Mike's subsequent 'task spawn E-1434' with 'already active in session N'.
