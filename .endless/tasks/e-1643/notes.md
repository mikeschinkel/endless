## Justification

The deliverable is a policy decision (remove vs warn vs keep the named-alternate worktree capability) that had to be resolved BEFORE any code task could be scoped. The investigation spanned worktree creation, the glob/regex readers, the reaper, sandbox naming, locks, and respawn, and produced a cross-cutting decision (ED-1515) plus the actual implementation task (E-1655). It cannot be inline in a do-task because the do-task's existence and scope are outputs of the research, not its premise.
