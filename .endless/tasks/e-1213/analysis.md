Detect via existing CLAUDECODE / ENDLESS_SESSION_ID env vars (no new vars).

Mechanisms: (1) startup tip when main has any dirty state pointing to 'endless worktree move'; (2) 'endless worktree move' command that lifts uncommitted changes from main into a fresh worktree, leaving main clean; (3) reject pre-commit hooks as a mechanism (not composable, agreed elsewhere).

Goal: zero-friction adoption — users get value without changing behavior, and behavioral shifts happen voluntarily as they see the tooling.

Note: 'move' chosen over 'extract' (read as removing FROM a worktree), aligns with git's vocab via 'git worktree move'.
