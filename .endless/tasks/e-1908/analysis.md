# Root cause (measured, not inferred)

Both slow tests follow this order:

```go
tmp := t.TempDir()
t.Setenv("XDG_CACHE_HOME", tmp)
t.Setenv("HOME", tmp)          // <-- the problem
...
build := exec.Command("go", "build", "-o", bin, "../../cmd/endless-go")
build.CombinedOutput()          // livewriters_test.go:117 and :165
```

`go env GOCACHE` is derived from HOME on macOS:

- real HOME  -> `~/Library/Caches/go-build` (warm; the same cache every other
  build in the repo uses)
- HOME=tmp   -> `<tmp>/Library/Caches/go-build` (empty, and thrown away with
  the test's TempDir)

Measured in this worktree:

| build | elapsed |
|---|---|
| `go build ./cmd/endless-go` with real HOME | **3s** |
| same build with HOME redirected to an empty temp dir | **still running at 60s** (capped) |

GOMODCACHE is unaffected (GOPATH-derived), so nothing is re-downloaded — this is
pure recompilation of every dependency, `modernc.org/sqlite` being the expensive
one.

The goroutine dump that made this look like a deadlock is just
`CombinedOutput`'s pipe copy waiting on a compiler that has not finished:
`livewriters_test.go:166` is the `go build` line, not the `destroy` invocation.

# Why the whole package blows its timeout

Two tests, each paying a full cold compile into a cache that is discarded
immediately afterward (each `t.TempDir()` is unique, so they cannot even reuse
each other's). That is what consumes the 10-minute package timeout and takes
`go test ./internal/...` down with it.

# The correct pattern already exists in this package

`internal/sandboxcmd/destroy_test.go` never redirects the test process's own
environment. It builds via the shared `buildSandboxBinary(t)` helper with the
ambient env intact, then scopes the redirect to the child only:

```go
cmd.Env = append(cmd.Environ(), "XDG_CACHE_HOME="+tmp, "HOME="+tmp)
```

`livewriters_test.go` already does exactly this for its own `destroy` command
(lines 123-125, 171-173). The `t.Setenv` pair at the top of each test exists
only because `Provision(...)` runs IN-PROCESS and reads `XDG_CACHE_HOME`. So the
in-process redirect is needed for `XDG_CACHE_HOME`; the `HOME` redirect is the
part that costs the build cache.

# Scope check

Only these two tests combine an in-test `go build` with a `t.Setenv("HOME")`.
Five other files shell out to `go build` in tests
(`internal/monitor/worktreecheck_scripts_test.go`, `internal/eventcmd`,
`internal/templatecmd`, `internal/sessionquerycmd`, and `destroy_test.go`
itself); none of them redirect HOME, so none are affected. This is a
two-test defect, not a systemic one.
