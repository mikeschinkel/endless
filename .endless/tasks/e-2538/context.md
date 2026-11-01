JetBrains IDEs index and watch every directory under the project root
unless it is marked Excluded, and the Go plugin runs a module sync for
every go.mod it finds. With Endless's .endless/worktrees holding one full
checkout per task (11 GB and 570,000 files on 2026-11-01), that costs
minutes of indexing and continuous CPU, registers each worktree as a VCS
root, and runs `go list` and `go mod download` in checkouts the developer
is not working in. Marking .endless/worktrees as Excluded by hand fixes it
for one project on one machine, and nothing remembers to do it for the
next one.

The exclusion is additive: a JetBrains module's .iml holds excludeFolder
elements inside its content element. The IDE watches and reloads that
file, and also rewrites it on its own schedule, so an edit must be
idempotent and leave every other byte alone. The .idea directory and the
.iml may be absent, gitignored, or committed.
