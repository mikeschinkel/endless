Found while verifying E-2550 in go-cliutil on 2026-11-06.

A `runner = "gotest"` check composes `go test -json ./...`. Under `-json`,
Go reports a build or setup failure inside the JSON stream on stdout, as
`build-output` actions followed by a package-level `fail`, and writes
nothing to stderr. For example, when a worktree picked up the main
checkout's go.work:

    {"ImportPath":"./...","Action":"build-output","Output":"pattern ./...:
    directory prefix . does not contain modules listed in go.work or their
    selected dependencies\n"}
    {"Action":"output","Package":"./...","Output":"FAIL\t./... [setup failed]\n"}
    {"Action":"fail","Package":"./...","FailedBuild":"./..."}

The runner's gotest-json normalizer counts no test failures from that, so
the run is refused with:

    verify: check exited non-zero but reported no test failures; meta:
    check=0, runner=gotest, exit=1, stderr=

The refusal shows an empty stderr and nothing from stdout, so the one line
that names the cause is invisible. Diagnosing it took reproducing the
command by hand under a stripped environment.

Two ways to surface it, which can both be done:

1. The refusal includes the tail of the check's stdout beside stderr, as
   `tailBytes` already caps stderr.
2. The gotest-json normalizer treats a package-level `fail` carrying
   `FailedBuild`, or any `build-output` action, as a failure of its own,
   reported with the build output as its message, so the run fails as a
   test failure with the cause in the report rather than being refused.

Relevant code: internal/verify/driver.go (runAndNormalize, tailBytes),
internal/verify/gotest.go (the gotest-json normalizer), and wherever the
"exited non-zero but reported no test failures" refusal is raised.
