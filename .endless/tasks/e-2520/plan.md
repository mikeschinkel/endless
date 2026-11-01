# Embed doterr.go instead of importing the go-doterr module

## Code
1. Find the packages that import the module: `git grep -l '"github.com/mikeschinkel/go-doterr"' -- '*.go' | xargs -n1 dirname | sort -u`.
2. In each of those packages, create a `doterr.go` (an empty file with the package's own `package` clause is enough), then run go-doterr's `make sync DIR=<this worktree>` from the go-doterr checkout so every copy is written from go-doterr's `doterr.go`. Never hand-edit a copy.
3. In each package, drop the go-doterr import and the `doterr.` qualifier from every call (`NewErr`, `WithErr`, `StringKV`, and anything else the grep finds). Calling them unqualified is the reason for one copy per package.
4. Remove go-doterr's `require` and `replace` lines from go.mod and run `go mod tidy`.
5. Each copy stamps its errors with its own copy ID, and `WithErr` wraps an error from a different copy in `ErrCrossPackageError`. Check every `WithErr` call: if one enriches an error built by another Endless package, change it so it doesn't (for example, wrap with `NewErr` and pass the error as the cause), and add a test that fails if `ErrCrossPackageError` appears there.
6. `GOWORK=off go build ./...`, `go vet ./...` and the Go tests pass. (Other `replace` lines may still need sibling checkouts; removing those is not this task.)

## Rule
7. Run `endless decision add` once from each of the endless, gomion and go-doterr projects, with the same text: go-doterr is a drop-in, never a module dependency. No go.mod may require or replace github.com/mikeschinkel/go-doterr, except the go-doterr repo's own test/, examples/ and cmd/ modules. A package uses doterr by holding its own copy of doterr.go, synced from go-doterr, so its calls need no package qualifier. go-doterr's doterr.go changes only with the owner's explicit approval.
8. The CLAUDE.md in the go-pkgs parent directory (beside the go-doterr checkout) is in no git repo and outside this worktree, so draft the same rule as a short subsection under "Cross-Package Dependencies" and give the exact text to the owner to paste.
