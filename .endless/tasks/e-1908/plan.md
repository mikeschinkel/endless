# Plan: stop the sandboxcmd destroy tests from cold-building the world

## Fix

Preferred: **build once per package, before any environment redirect.**

1. Promote `buildSandboxBinary` (already in `destroy_test.go`) to a
   package-level once-built helper — a `sync.Once` guarding a single build into
   a package-scoped temp dir, returning the same path to every caller. Building
   happens with the ambient environment, so it reuses the developer's warm
   `GOCACHE`.
2. Point `livewriters_test.go:117` and `:165` at that helper instead of their
   own `exec.Command("go", "build", ...)` calls.
3. Drop `t.Setenv("HOME", tmp)` from both tests. Keep
   `t.Setenv("XDG_CACHE_HOME", tmp)` — `Provision(...)` runs in-process and
   needs it. If `Provision` also consults HOME, set HOME via the existing
   child-only `cmd.Env` lines rather than in-process; confirm which it reads
   before deciding.

Acceptable smaller alternative if the once-per-package refactor is unwanted:
give the build command an explicit `GOCACHE` captured before the redirect
(`build.Env = append(os.Environ(), "GOCACHE="+realGOCACHE)`). Keeps the change
to two lines but leaves two full builds in place.

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
  `go build` — a grep guard so the pattern cannot come back. Both current call
  sites are in `internal/sandboxcmd`.
- Fold-in regression: `go test ./internal/...` completes without a package
  timeout panic. This is the whole point of the task and is the one check that
  would have caught the defect originally.

## Out of scope

The other five in-test `go build` call sites. They do not redirect HOME and are
not affected; consolidating them onto one shared build helper is a separate
cleanup if anyone wants it.

