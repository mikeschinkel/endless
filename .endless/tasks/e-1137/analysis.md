writes from a worktree go to the worktree's project files; writes from main go to main's. Affects project_config_path() and project_verbs_path() in src/endless/matchers.py and any callers that assumed E-1111's anchor-to-main semantics.

Verification: from a worktree, run 'endless verb add foo'; only the worktree's .endless/verbs.json is modified, not main's; the new verb still surfaces in subsequent reads via project + machine layer merge.
