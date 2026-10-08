# Plan: stop a redirected HOME from cold-building Go in tests and the verify runner

## Fix

Preferred: **build once per package, before any environment redirect.**

1. Promote `buildSandboxBinary` (already in `destroy_test.go`) to a
   package-level once-built helper — a `sync.Once` guarding a single build into
   a package-scoped temp dir, returning the same path to every caller. Building
   happens with the ambient environment, so it reuses the developer's warm
   `GOCACHE`.
2. Point both `go build` calls in `livewriters_test.go` (grep `exec.Command("go", "build"`)
   at that helper instead of their own builds.
3. Drop `t.Setenv("HOME", tmp)` from both tests, and from
   `TestDestroyExistingWithIfExistsStillDestroysAndReports` in `destroy_test.go`
   (folded in from E-2219: same cause, a third test). Keep
   `t.Setenv("XDG_CACHE_HOME", tmp)` — `Provision(...)` runs in-process and
   needs it. If `Provision` also consults HOME, set HOME via the existing
   child-only `cmd.Env` lines rather than in-process; confirm which it reads
   before deciding.

Acceptable smaller alternative if the once-per-package refactor is unwanted:
give the build command an explicit `GOCACHE` captured before the redirect
(`build.Env = append(os.Environ(), "GOCACHE="+realGOCACHE)`). Keeps the change
to two lines but leaves two full builds in place.

## Folded in: the verify runner (E-2266)

The runner has the same defect in product code. `isolatedEnv` in
internal/verifycmd/env.go replaces `HOME` for every suite, so any suite that runs
`go test` or `go build` compiles against an empty `GOCACHE` under the per-run
temp HOME, and uv's cache is cold the same way. E-2266 found three verify
failures this explains (E-1904, E-2186, E-1889): ~70s suites taking ~10 minutes
and deadline tests missing under load.

4. Before replacing `HOME`, resolve the caller's `GOCACHE` (`go env GOCACHE`
   when `go` is on PATH) and uv's cache dir, and pass them to the suite
   explicitly — unless the caller already set them. These are build caches, not
   config, so ED-1583's isolation goal (no real config or database reachable) is
   unaffected; the `_guard.sh` isolation check still passes.
5. Unit test in `internal/verifycmd`: the suite env carries the caller's
   `GOCACHE` and still carries the temp `HOME`/`XDG_CONFIG_HOME`.

Verification adds: a suite that runs `go env GOCACHE` through the runner sees
the caller's cache path, not one under the temp HOME.

## Verification

`tests/tasks/e-1908-verify.sh`:

- **The regression, as a wall-clock budget.** `go test ./internal/sandboxcmd/
  -timeout 180s` completes well inside its timeout. Assert on exit status, and
  print the elapsed time so a regression is visible rather than merely slow.
  Baseline for the report: the package currently cannot finish in 10 minutes.
- The four behavioral assertions these tests make must be unchanged — destroy
  refuses with a live writer and names the PID and `--force`; `--force`
  destroys and prints the confirmation. Do not weaken the tests to make them
  fast; the point is to stop paying for a cold compiler, not to stop testing.
- Assert no `_test.go` in the repo combines `t.Setenv("HOME"` with an in-test
  `go build` — a grep guard so the pattern cannot come back. The current call
  sites are all in `internal/sandboxcmd`.
- Fold-in regression: `go test ./internal/...` completes without a package
  timeout panic. This is the whole point of the task and is the one check that
  would have caught the defect originally.

## Out of scope

The other five in-test `go build` call sites. They do not redirect HOME and are
not affected; consolidating them onto one shared build helper is a separate
cleanup if anyone wants it.



