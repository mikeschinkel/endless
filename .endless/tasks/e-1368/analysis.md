Each endless-* binary on startup walks up from cwd looking for an endless-managed worktree (e.g. .endless/worktree.json marker) and the corresponding ~/.cache/endless/sandboxes/worktree-e-NNN/ — if both present, self-set ConfigDir to that path before any consumer reads it.

This eliminates the two-layer architecture (wrappers + binaries) that caused the bin-sandbox/endless-event-execs-stale-global-binary bug observed during E-1322 verification.

Reuses the existing monitor.IsSandboxActive() pattern, reversed: instead of detecting whether a caller routed us into sandbox, the binary routes itself.

Removes one hyphenated dir; no PATH manipulation needed; works identically for shell-invoked and hook-fired calls.
