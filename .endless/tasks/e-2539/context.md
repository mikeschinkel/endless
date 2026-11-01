VS Code with the Go extension runs gopls over the whole workspace, and
gopls loads every Go module it can see. With Endless's .endless/worktrees
holding one full checkout per task, that means dozens of copies of the
same module, each with its own go.mod, loaded, watched and type-checked
while the developer works in one of them. VS Code's own file watcher and
search also walk the whole tree.

The JetBrains counterpart (a sibling task under this epic) has a known
mechanism. The VS Code one does not: it is unclear which of
files.watcherExclude, search.exclude, files.exclude and gopls's
directoryFilters setting each solve which part of the problem, whether a
multi-root workspace changes the answer, and whether writing
.vscode/settings.json (JSON with comments) from outside the editor is
picked up live or fights the editor's own writes. Settings may also live
in a .code-workspace file rather than .vscode/settings.json.
