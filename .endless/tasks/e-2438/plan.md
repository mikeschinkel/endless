## Approach

Add `Dir string \`toml:"dir"\`` to `Check` in `internal/verify/check.go`. In `validateCheck`, refuse an absolute dir, one that is not an existing directory, or one whose resolved path (`filepath.EvalSymlinks`) is not inside the repo root, each with its own sentinel in `internal/verify/errors.go` and the check index.

In `internal/verifycmd/env.go`'s check loop, compute the check's working directory (root joined with `chk.Dir`, or root) and pass it to `driver.Run` in place of root. Every driver already hands that argument to `execCapture` as `cmd.Dir`, so gotest, pytest, generic and sh all follow without driver changes; gotest's and pytest's paths then resolve against it.

In the pytest driver's report parsing, prefix each test's suite with `chk.Dir` when set. Leave setup and teardown (`runSetup`) at the root.

Document `dir` in the suite-rules text (`src/endless/suite_rules.py`) and the verify section of `docs/guide/orchestration.md`, with an example running `go test` in a nested module (`dir = "dtx"`).
