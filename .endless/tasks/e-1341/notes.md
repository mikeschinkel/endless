E-1309's fix (env sanitization + worktree-detection guard) works regardless of how git got steered to the wrong repo, but we haven't identified the actual mechanism.

ENDLESS_DEBUG_GIT=1 trace logging was added in E-1309 as instrumentation for the next occurrence.
