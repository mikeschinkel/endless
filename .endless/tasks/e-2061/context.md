`worktree land` migrates the real DB at Step 5.5, and the Justfile refreshes the installed endless-go only after land returns.

E-1969's land proved the window is entered: a Claude hook fired ReapWorktreesForProject mid-land and logged 50 'no such column: active_task_id' lines, one per worktree, reading to the operator as a failed land.

Harmless only because the failure was a READ that fails open and a rename makes old code error rather than misbehave — neither is guaranteed.
