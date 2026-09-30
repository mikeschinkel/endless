The companion file (E-989) has a worktree_path field that is currently never set — writeClaudeCompanion always leaves it empty.

This makes session use (E-1014) and session cd (E-990) fall back to cwd, which for sessions started outside a worktree (like the bootstrap session running in main) does not point at the worktree the user actually wants to test.
