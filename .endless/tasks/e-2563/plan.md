# Give every Go task worktree its own go.work

Go looks for a `go.work` by walking up from the current directory, and a task worktree lives inside its main checkout. A `go.work` in the main checkout is therefore found from the worktree, and Go refuses to build there because the worktree's modules are not listed. A `go.work` in the worktree itself is nearer, so Go uses it instead.

## Core: a generic extension point

1. Add a worktree extension point to Endless's core. It runs after a task worktree is created (in `_bootstrap_task_worktree`, before the project's post-worktree-create hook) and before a session starts in an existing worktree on `task spawn`, `task claim` and `session resume`.
2. The core knows nothing about any language. Extensions register with it, and each decides for itself whether it applies. A failing extension prints an error naming itself and the worktree, and does not stop the spawn, claim or resume.

## Go support: a module outside the core

3. Add a Go-support module that registers with the extension point. It is built into Endless but kept separate from the core, so it can later move to Gomion or call it.
4. It applies to a worktree that has any `go.mod`, ignoring `.endless/`, `vendor/` and `testdata/`. It does not read `projects.language`.
5. If the worktree has no `go.work`, it writes one with `go work init` and `go work use` for every module found, including nested `test/` and `examples/` modules. Go sets the file's `go` line. A worktree that already has a `go.work` is left alone; that covers a tracked one such as go-tealeaves's and the one Endless's own create hook generates.
6. When the file it wrote is not covered by the repo's `.gitignore`, it prints a warning telling the user to add `go.work` and `go.work.sum` to `.gitignore`. It never writes to `.git/info/exclude`.

## Existing worktrees

Worktrees created before this change get their `go.work` the next time a session is spawned, claimed or resumed in them.

## Verification

Tests cover: a new Go worktree under a main checkout with a `go.work` builds; a nested `test/` module is listed; an existing `go.work` is untouched; a non-Go worktree gets nothing; an existing worktree without the file gets it on resume; the warning appears only when `.gitignore` does not cover `go.work`.
