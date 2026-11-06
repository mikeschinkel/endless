## Decisions made with Mike while planning

- Write the file at creation and again whenever a session is spawned, claimed or resumed in a worktree lacking one, rather than once for existing worktrees. That also covers a file deleted later, at the cost of one file check per start.
- Write it in every Go worktree, not only when the main checkout has a go.work, so a main go.work added later (by Gomion's go.work command) needs no further step, and Gomion does not have to know Endless's worktree layout.
- Go support is not Endless core functionality. The core gets a generic extension point, and a separate built-in module supplies the Go behavior, so it can later move to Gomion.
- No .git/info/exclude: a hidden entry leaves users unaware that their .gitignore is missing go.work. The module warns instead. Mike's own repos missing the entry get it through their next tasks.

## Costs accepted

A Go worktree runs in workspace mode, so `go mod vendor` refuses there (no repo vendors today), and if a branch raises go.mod's Go version, Go refuses until `go work use` is run; its error names that fix. Endless detects Go by go.mod because projects.language is unreliable: it lists Endless as bash, and many projects have it empty.
