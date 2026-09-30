The auto-commit step for verbs.json reads main's verbs.json, performs additive set-union on the value key against the worktree's verbs.json, writes the deduped result, and commits to the worktree's branch. If the post-dedup result equals main's content, skip the commit (no-op). All in Go code (cmd/endless-event or wherever the land flow lives); no .gitattributes merge driver.

Verification: two worktrees independently add the same verb; first lands cleanly; second lands with auto-commit producing a no-op for verbs.json.
