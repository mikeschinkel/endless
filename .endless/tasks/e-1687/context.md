From a bare (non-Claude) shell inside a self-dev worktree, `endless ...` resolves to the global Python CLI (~/.local/bin/endless, editable from the MAIN checkout) and `endless-go` to /usr/local/bin/endless-go (main's build) — so new flags/behavior in the worktree silently aren't exercised (e.g. `endless session next --tree` errors with 'No such option' until landed).

Worktree candidate code is only reached via ./bin/endless-go directly or PYTHONPATH=src.
