Two reusable packages in the go-pkgs collection would carry the IDE-specific
file handling, so gomion and any other Endless-managed project can share
it:

- go-jetbrains: locate the module .iml (in .idea/ or at the project root,
  depending on the project's age), and add an excludeFolder entry under
  the content element if absent, byte-identical otherwise. Never create
  .idea/ or the .iml.
- go-vscode: read .vscode/settings.json as JSON with comments and trailing
  commas (a strict parser will not do), add entries to files.watcherExclude,
  search.exclude and gopls.directoryFilters ("-.endless/worktrees") if
  absent, preserve formatting elsewhere. Never create .vscode/.

Endless calls both from the project-init path and, ideally, from worktree
creation, since a developer may open an IDE on a project that was
registered before this shipped. Which Endless command owns the call is
open: register, a new `endless ide` verb, or every worktree claim.

Leave .endless/db-ledger alone or make it opt-in: JetBrains already skips
deep indexing of files over about 2.5 MB, and excluding the ledger removes
task history from IDE search for little gain.

A running IDE is the hazard: JetBrains reloads external .iml edits but also
rewrites the file itself. Additive, idempotent edits survive that; a whole-
file rewrite eventually loses a race.
