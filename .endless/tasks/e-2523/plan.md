Remove every replace directive from Endless's go.mod and require each go-pkgs module by its newest published tag, as released by E-2209 and its blockers. Delete the stray go-gitutil replace outright. Run go mod tidy.

Add a Go test that reads go.mod and fails when it holds any replace directive, so a replace cannot return unnoticed.

Retire `just go-work-init`: it builds go.work from go.mod's replace lines, so with none left it does nothing useful. Remove the recipe, its call from `just install`, its call from .endless/hooks/post-worktree-create.sh, and the comments that describe it. E-2563 now gives each task worktree its go.work, and E-2561 manages go.work in main checkouts for development across repos.

Verify with GOWORK=off go build ./..., GOWORK=off go vet ./..., GOWORK=off go test ./... and the Python test suite. A failure means a needed change never reached a published tag: report it, and don't restore a replace.
