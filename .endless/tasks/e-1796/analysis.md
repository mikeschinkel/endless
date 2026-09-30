(1) A session does not stop at its bound task's implementation — it closes the discovery loop: walk session status, chase blockers, file+plan follow-ups. Document in the happy path as the reconciliation with one-worktree-per-task so agents don't over-index on 'one session == one task' and wrongly tell users to stop.

(2) Don't passively accept a block: if the dependent needs only a small independent slice of the blocker, extract that slice and link relates_to instead of blocked_by.

Guide-only; the README restructure and the link-command over-blocking gates are separate tasks.
