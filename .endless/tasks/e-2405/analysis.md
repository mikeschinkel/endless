## Why from the task

A task records its project, and a project records its directory. Nothing about
where a task's worktree lives needs the caller's cwd. Taking it from cwd
creates a worktree and branch in the wrong repository, runs the wrong project's
hooks, and tags the events with the wrong project, so the wrong project's
ledger gets them.

## Why claim refuses across projects and spawn does not

Spawn binds no session before launch: it does the pre-launch work, and the new
session binds itself on startup, in the task's worktree. So spawn can do all of
it in the task's project. Claim binds the calling session, which is already
running in another project: its recorded project, its agent's working directory
and permissions, and the project config its hooks read all belong to that
other project, and claim cannot move them. Spawn is the command that starts a
session where the task lives, so claim names it.

## Missing directories

A registered project whose path does not exist here, because it was registered
from another machine or its checkout moved, is refused rather than falling back
to cwd. Falling back would recreate this bug.
