E-985 surfaced a Go-specific worktree friction (go.mod replace directives use ../go-pkgs/... which doesn't resolve from inside .endless/worktrees/<id>/). The fix for Go is one specific solution, but every language/project will have its own worktree-bootstrap concerns: Node needs npm install or symlink to node_modules; Python may need venv recreation; Rust may need target/ cleanup; etc. Endless cannot bake all of these in.

Make worktree-bootstrap pluggable via a project-owned hook. Proposed mechanism: when 'endless worktree' creates a new worktree (per E-971's design), it looks for and runs '<project-root>/.endless/hooks/post-worktree-create.sh' (or similar) inside the freshly-created worktree directory. The script is project-owned, version-controlled, and free to do anything: symlink, install deps, run language-specific tooling.

Endless ships no default hook. Each project (or its AI assistant) writes the hook appropriate to its stack. For endless-on-endless, the hook would symlink ~/Projects/go-pkgs into the worktree's parent dir to satisfy the go.mod replace paths.

Acceptance: (a) hook discovered at a documented predictable path, (b) executed with worktree path as argument and cwd, (c) hook failure is surfaced but non-fatal (worktree still created), (d) E-985's manual symlink replaceable by a documented hook script.

Linked to E-985 (the Go-specific symptom that motivated this generalization).