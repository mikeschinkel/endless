_resolve_endless_go() (or land_worktree) should prefer the worktree-built endless-go during a land regardless of --db, since the worktree binary's embedded schema always matches the rows apply-change just inserted.

Belt and suspenders: move the apply-change loop out of the Justfile into endless worktree land itself so apply-schema then advance-main then record-landing is one command that always uses the right binary internally, leaving just land a thin build+call+rebuild wrapper.
