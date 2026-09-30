Landing E-1898 on 2026-08-10 produced two failures in the land recipe itself.

First, `just land` rebuilds the worktree binary but never checks the worktree's SOURCE is current with main, so a branch 147 commits behind rebuilt a still-stale binary and aborted on an unrelated enum-drift error naming gate_kinds. Second — and the expensive one — apply-change then SUCCEEDED and the merge failed, leaving the real database migrated to a schema no installed binary understood; session tracking froze machine-wide and recovery needed a manual restore.

The justfile only reasons about apply FAILING ("the land aborts before main advances"); apply succeeding and the merge failing is the unhandled, irreversible case.
