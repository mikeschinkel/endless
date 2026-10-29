# Plan: Raise go directive to 1.27 for go-cfgstore

endless's go.mod replaces go-cfgstore with the local checkout. go-cfgstore's E-2412 raises its go directive to 1.27.0; a main module whose go line is below a dependency's stops with "go: updates to go.mod needed". Raising endless's own line first means endless never sees that window.

## Steps

1. In endless's go.mod, set the go directive to 1.27.0 with `go mod edit -go=1.27.0`. Do not run `go mod tidy`: the go line alone is enough, and tidy against the local go-pkgs checkouts can rewrite requires from those repos' uncommitted work.
2. Build endless against go-cfgstore's main checkout as it is, and against E-2412's branch through a scratch `-modfile` that points the go-cfgstore replace at that worktree.
3. Run endless's test suite.
4. Land before E-2412 lands; E-2480 blocks E-2412.
