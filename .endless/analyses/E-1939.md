Remove the web dashboard entirely. It is a proof-of-concept that will not be
evolved: web work was deliberately halted to focus on the harder database,
hooks, and workflow problems, and every task targeting it has been obsoleted.
Keeping it costs a whole build toolchain and dependency surface that nothing
else in Endless needs.

Scope by inspection, to be re-verified at implementation time rather than
trusted from this description:

- The `internal/web` package tree (components, pages, templates, data, static,
  assets) and `internal/servecmd`, which is its only caller.
- The `serve` subcommand dispatch in the Go entrypoint, and the Python `serve`
  command that shells out to it including its port option and binary-watch
  restart loop.
- Justfile recipes and their invocations from the aggregate build: templ
  generation, the tailwind CSS build, the templUI symlink helper, and the dev
  watcher recipes that run them. The aggregate build recipe and the help text
  both name these and must stop doing so.
- Go module requirements that exist only for the web stack: the templ runtime
  and templUI. Check goldmark separately — the terminal renderer in
  `internal/mdterm` uses it independently and must keep working. Re-vendor
  after pruning.
- Tracked generated artifacts: the `*_templ.go` files and the built CSS output.
  Also the land-time handling that exists because these are build-regenerated
  and git-tracked.
- External tooling assumptions: templ and tailwindcss as prerequisites for
  building the project.

Verify afterward that a clean build, the full test suite, and vendoring all
succeed with the web toolchain absent, and that no remaining code imports the
removed packages. `internal/mdterm` carries a comment describing itself in
terms of the web markdown renderer; reword it so it does not reference a
package that no longer exists.

Deliberately out of scope: anything about a future web UI. When a web surface
is wanted again it will be preceded by research into whether Endless should
host it at all or whether a different tool should, with Endless integrating.
Do not file follow-on work for that here.
