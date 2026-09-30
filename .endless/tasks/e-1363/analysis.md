Update verb_add (and any other writers) to write there instead of <project>/.endless/verbs.json.

Remove the direct-commit-to-main step from verb_add — the side location doesn't need a commit, and project main only receives verbs.json via worktree land going forward.

Read sites (title validation, etc.) read from the new location.

Cross-machine note: verbs added between lands are machine-local until a land carries them to project main; acceptable per the design decision.
