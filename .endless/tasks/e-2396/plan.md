# Keep branch code from ever opening the main database

1. Reproduce: run each suite with HOME/XDG_CONFIG_HOME pointing at a decoy
   home that holds a copy of a main database one version behind the branch.
   Find every test (Python and Go) that opens the decoy, and fix each one
   to use its isolated config.
2. Make the guard independent of where the binary sits: decide "this is not
   the installed release" from what the build is, not its path. For example,
   stamp the build (ldflags) with its source checkout, and treat any build
   whose source is a task worktree as a worktree build. `go test` binaries
   count as worktree builds whenever the package under test lives in one.
3. Refuse a forward migration of main by anything other than the land path
   (the migration binary `worktree land` builds) and the installed release.
4. Tests: a copied binary, a temp-dir conftest build, and a `go test` process
   in a worktree all refuse main, each with the ED-1601 message.
