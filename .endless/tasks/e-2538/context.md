JetBrains IDEs index and watch every directory under the project root
unless it is marked Excluded, and the Go plugin runs a module sync for
every go.mod it finds. With Endless's .endless/worktrees holding one full
checkout per task (11 GB and 570,000 files on 2026-11-01), that costs
minutes of indexing and continuous CPU, registers each worktree as a VCS
root, and runs `go list` and `go mod download` in checkouts the developer
is not working in. Marking .endless/worktrees as Excluded by hand fixes it
for one project on one machine, and nothing remembers to do it for the
next one.

VS Code has the same problem through gopls, which loads every module it
can see unless gopls.directoryFilters excludes the directory, and through
its file watcher and search, which have their own exclude settings.

The exclusion is additive: a JetBrains module's .iml holds excludeFolder
elements inside its content element, and VS Code's .vscode/settings.json
holds files.watcherExclude, search.exclude and gopls directoryFilters.
Both IDEs watch and reload those files, and JetBrains rewrites the .iml on
its own schedule, so an edit must be idempotent and leave every other byte
alone. The IDE project files may be absent, gitignored, or committed.
