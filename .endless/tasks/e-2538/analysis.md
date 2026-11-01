A reusable go-jetbrains package in the go-pkgs collection carries the file
handling, so gomion and any other Endless-managed project can share it:
locate the module .iml (in .idea/ or at the project root, depending on the
project's age), and add an excludeFolder entry under the content element
if absent, byte-identical otherwise. Never create .idea/ or the .iml.

Endless calls it from the project-init path and, ideally, from worktree
creation, since a developer may open an IDE on a project that was
registered before this shipped. Which Endless command owns the call is
open: register, a new `endless ide` verb, or every worktree claim.

Leave .endless/db-ledger alone or make it opt-in: JetBrains already skips
deep indexing of files over about 2.5 MB, and excluding the ledger removes
task history from IDE search for little gain.

A running IDE is the hazard: JetBrains reloads external .iml edits but also
rewrites the file itself. Additive, idempotent edits survive that; a whole-
file rewrite eventually loses a race.
