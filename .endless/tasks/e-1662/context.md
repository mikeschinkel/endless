Result: the PostToolUse hook and sandbox CLI fall back to the GLOBAL/main-checkout binary instead of the worktree's candidate build -- defeating the per-worktree sandbox's purpose of exercising candidate code (E-1281/E-998).

Observed in worktree e-1643: bin-sandbox/ present, bin/ absent, settings.json has XDG but no hook block and no PATH-prepend.

This is also what left e-1643 exposed to the stale-global-binary integrity skew.
