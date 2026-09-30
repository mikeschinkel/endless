Approach: per-session git worktrees so sessions are filesystem-isolated; main becomes a sacred clean integration target; behavioral gate (PIVOT trigger + UserPromptSubmit hook) catches topic shifts before edits leak between scopes.

Full design in .endless/plans/E-968.md.
