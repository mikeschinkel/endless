Problem: today, exercising new hook code in a real Claude session requires temporarily repointing the global ~/.local/bin/endless-hook symlink at the worktree's bin/endless-hook. This is risky: every other live Claude session on the machine runs the unverified binary for the duration of the swap. We hit this during E-989 verification.

Claude Code has no CLI flag or env var to point at a custom settings file (confirmed against the official 4-tier precedence model: Managed → Command line → Local → Project → User; none accept a path override). However, project-level .claude/settings.json takes precedence over ~/.claude/settings.json for hook configuration. That gives us a clean lever inside a worktree.

Proposal: when endless creates a worktree (or as part of an explicit 'endless worktree prep-test' command), generate a .claude/settings.json under the worktree root that overrides the hook command path to the worktree's bin/endless-hook. Any Claude session spawned with cwd inside that worktree picks up the override automatically; sessions outside the worktree are unaffected.

Verification:
- From a worktree with the override settings file: spawn Claude there; SessionStart writes via the worktree's binary; main checkout's hook is untouched.
- Concurrent main-checkout Claude session: continues using its own hook unaffected.
- Worktree removed: the override is removed with it.

Open design items:
- Generated vs hand-edited: should the file be git-ignored (per-worktree generated) or committed as a template? Probably ignored — the absolute path to bin/endless-hook is worktree-specific.
- Should the hook binary be auto-rebuilt before the override is wired up, or assumed already built? Probably auto-build on the first override invocation.
- Interaction with E-971's worktree subsystem: this should land as part of that workstream rather than a standalone command, if the timing aligns.

Origin: E-989 verification round-trip. Filed because the symlink-swap workaround is unacceptable as a recurring pattern.