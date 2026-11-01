`endless worktree land` refuses to land while the main checkout has
uncommitted user changes. JetBrains IDEs (GoLand, and IntelliJ IDEA with
the Go plugin) keep producing exactly such changes without anyone editing:
whenever go.mod changes on disk, the Go plugin re-syncs modules by running
`go list -m -json all` and then `go mod download -json -x` in the project
root. When the committed go.mod is stale against sibling checkouts named in
`replace` directives, that sync rewrites go.mod and go.sum in place.

Landing E-2520 hit this twice on 2026-11-01. `git restore go.mod go.sum`
cleaned main at 03:22:31; the IDE log shows com.goide.util.GoExecutor
starting `go list -m -json all (endless)` at 03:22:39 and `go mod download
-json -x` at 03:22:40, and the files' mtime is 03:22:40. The land at
03:22:51 failed again with "main has uncommitted user changes". Closing
GoLand did not help because IntelliJ IDEA had the same project open. The
only working path was to quit the IDE, restore, and land back to back.

That workaround is expensive: a JetBrains IDE takes minutes and a lot of
CPU to reopen, and Mike works in one all day. The same sync can fire after
every land that changes go.mod, so the problem recurs with every module
bump. The IDE's write is also a legitimate tidy of a stale file, not a
mistake, which is why plain "the user must clean up" is the wrong framing.

Other IDE-driven writes to the main checkout (.idea/, run configurations,
gofmt-on-save) can trip the same gate, so the brainstorm should consider
the class, not only go.mod.
