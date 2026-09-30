_default_base_branch in src/endless/worktree_cmd.py uses git symbolic-ref refs/remotes/origin/HEAD, which is unset on fresh clones (it requires git remote set-head).

Filed during E-971 Layer F implementation.
