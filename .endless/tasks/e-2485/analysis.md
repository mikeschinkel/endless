Agreed with the user in session ES-1383:

- Check only at the hand-in, not in `task verify` or `worktree land`. A task
  already at `unverified` with an unapproved planned item (E-2217 today) would
  otherwise be stuck: its items are frozen, so it couldn't be approved without
  walking back through `revisit`. With the check at the hand-in, no new task
  reaches that state, and existing ones carry on as before.
- A grown item the user has kept by answering its reconciliation question
  counts as approved; asking for a separate approval would ask twice.
- Fixing a task whose state is already wrong in other ways is E-2486's
  user-only correcting entry, not this task.
