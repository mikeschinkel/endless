Also remove '.endless/events/*.jsonl' from AUTO_COMMIT_GLOBS in src/endless/worktree_cmd.py — that pattern is dead since no events.jsonl files exist anywhere anymore.

In the wild (fresh clones) no one ever has a .endless/events/ to migrate from, so this code only existed to handle Mike's local environment, which is now settled.
