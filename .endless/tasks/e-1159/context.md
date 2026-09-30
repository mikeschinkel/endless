esu (eval $(endless session use)) exports ENDLESS_PROJECT_ROOT, ENDLESS_WORKTREE_PATH, ENDLESS_SESSION_ID, ENDLESS_HARNESS_SESSION_ID, ENDLESS_HARNESS into the current shell. There is no inverse — esp only cd's to the project root, it does not unset the vars.

Result: every subsequent endless command in that shell points at the activated session's worktree DB until the user opens a new shell or manually unsets all five vars.

Discovered 2026-05-03 verifying E-1114.
