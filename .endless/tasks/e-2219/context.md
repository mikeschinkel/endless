`internal/sandboxcmd` fails on main. `TestDestroyExistingWithIfExistsStillDestroysAndReports`
exceeds the 10-minute package timeout, so `just test-go` is red for every task
that runs it.

The cause is exact, and deterministic rather than a race. On macOS Go derives
`GOCACHE` from `$HOME` (`$HOME/Library/Caches/go-build`), not from
`XDG_CACHE_HOME`. This test calls `t.Setenv("HOME", tmp)` on the TEST PROCESS
before calling `buildSandboxBinary`, so its `go build` runs against an empty build
cache and cold-builds the whole binary and every dependency. Its two siblings in
the same file set `HOME` only in the subprocess's `cmd.Env`, which is why they
build in 12-13s against the warm cache while this one never finishes.

Confirmed on a pristine main checkout, with no worktree involved: the goroutine
dump puts the hang inside `buildSandboxBinary`, on its `go build` call.

`git log -S` places the `HOME` override in ac1844f63 (E-1367, which collapsed
seven binaries into one `endless-go`). That task is also what turned it from slow
into fatal: before it the test built a small single-purpose `endless-sandbox`;
after, it cold-builds the consolidated binary that links every subcommand. It was
survivable in May 2026 and is not now, because the binary kept growing.
