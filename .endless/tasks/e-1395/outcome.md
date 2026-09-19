Fixed in commit 31be00d on branch task/1395-fix-tmux-window-pane-ids-scope (split off the E-1381 branch and rebased on origin/main). Worktree at ~/Projects/endless/.endless/worktrees/e-1395.

E-1381 branch is now back to d3093f1 (just the docstring + test-realism refinements), no longer carrying the unrelated E-1395 fix.

Verification:
- Function-level (REPL): from pane %150, _tmux_window_pane_ids() now returns ['%150','%151'] (correct window @84) instead of unrelated panes.
- Python test suite: 383/383 pass.
- Implicit e2e: session 451 (pane %148, E-1378 worktree) successfully ran 'endless task bind' to rebind itself to E-1378 after the fix landed. That bind path goes through _current_endless_session_id → _read_live_companions, and the pre-fix bug would have produced wandering attribution. Successful bind confirms the resolver now lands on the correct window's companion.

Follow-up tasks filed:
- E-1397: fail loudly in non-SessionStart hooks when preconditions are missing
- E-1398: add endless setup verb to install/verify claude-hook integration
- E-1399: repair historical event misattribution caused by this bug