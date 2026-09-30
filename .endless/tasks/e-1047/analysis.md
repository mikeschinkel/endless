Silent success is wrong here: these commands aren't run frequently enough to justify silence.

Design: status line to stderr (so it doesn't pollute stdout-eval'd output) — '• Session <id> -> <path>' on success; when companion's worktree_path is set but the directory is missing (E-1038's silent fallback), prepend '! worktree <path> no longer exists; falling back to cwd' first.

Applies to both session_use_resolve and session_cd_resolve.
