Brings Claude's `isolation:'worktree'` semantics into alignment with endless's tracker.

Open concerns from the 2026-06-12 background-agents research report: (1) the hook is documented as a non-git-VCS adapter, not a tracker bridge — using it as such is supported-but-off-label; (2) version-fragile (pin Claude Code version, re-test on upgrade); (3) open GitHub issues around stability. Decide adoption when Agent View exits research preview.
