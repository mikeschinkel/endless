Endless manages projects whose developers work in an IDE all day, and the
IDE and Endless keep colliding in the main checkout. Two collisions showed
up while landing E-2520 on 2026-11-01:

- JetBrains IDEs (GoLand, IntelliJ IDEA with the Go plugin) index every
  worktree under .endless/worktrees, register each as a VCS root, and run
  a Go module sync (`go list -m -json all`, then `go mod download`) for
  every go.mod they find. With thirty-plus worktrees that is 11 GB and
  570,000 files of indexing and watching, and the IDE log fills with
  "Ignoring change under another root" warnings.
- The same module sync rewrites go.mod and go.sum in the main checkout
  whenever the committed file is stale against sibling checkouts named in
  replace directives, which trips worktree land's clean-tree gate (see
  the related brainstorm about landing despite IDE-written changes).

VS Code has the equivalent surface through gopls, which loads every module
it can see unless told otherwise, plus its own watcher and search settings.

Endless already owns the knowledge the IDE lacks: which directories are
worktrees, when they appear and disappear, and which one a session is
working in. Nothing today passes that knowledge to the IDE, so every
developer discovers the settings by hand, per project, per machine.

This epic collects the IDE-facing work so it is designed once, as a
product feature, rather than fixed one setting at a time. PRODUCT: on
another machine and project the IDE may be any of these or none, its
project files may be absent, gitignored, or committed, and Endless must
never create them or impose an editor on a project that chose none.
