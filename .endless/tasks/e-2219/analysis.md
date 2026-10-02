## The fix

Set `HOME` only where it is needed — the subprocess under test — exactly as the
other two tests in this file already do. The test process does not need a
redirected `HOME`; it needs the sandbox it provisions to land somewhere
throwaway, which `XDG_CACHE_HOME` already handles for Endless's own paths.

Check whether `Provision` reads `HOME` directly. If it does, the sandbox root has
to be redirected some other way than by moving the whole process's `HOME` — the
point is that no `go build` may inherit a redirected `HOME`.

## Look for the same shape elsewhere

Any test that sets `HOME` via `t.Setenv` and then shells out to `go build` has
this bug, silently paying a cold build. Worth a sweep: the symptom is a test that
is inexplicably slower than its siblings, and it will only get worse as the
binary grows.

A guard is worth considering so it cannot come back — a test asserting that no
`go build` in the suite runs with a redirected `HOME`, or a shared build helper
that scrubs `HOME` before invoking the compiler.

## Why not just raise the timeout

Because the cost is real and paid on every run. A cold build of the consolidated
binary is minutes, and raising the timeout makes `just test-go` slow instead of
red, which is harder to notice and worse to live with.

## Verification

- `go test ./internal/sandboxcmd/` passes, and that test completes in the same
  order of time as its two siblings rather than minutes.
- The test still proves what it was written to prove: `--if-exists` does not
  suppress the success message when the sandbox does exist.
