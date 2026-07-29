# Plan — E-1791: RunnerDriver interface + pytest/uv driver

## Context

E-1789 resolved the verify system's first-class-runner architecture (ED-1535/1536/1537,
full design in E-1789's `--outcome`). The current seam is a struct-of-funcs registry
(`firstClassRunner{translate,capture}` in `internal/verify/check.go`) that emits command
strings an outer shell runs. This task replaces it with a `RunnerDriver` Go interface
whose drivers **execute** their own runs, and adds the `pytest/uv` driver — which fixes
the original E-1605 defect (uv-managed project, `pytest` not on PATH). This unblocks
E-1605's pytest slice.

## Decisions this implements (do not re-derive; see the ADRs)

- **ED-1535** — three-level type system: (1) the `verify.toml` grammar stays HOW-agnostic
  (`runner = "<string>"`); (2) registered runner-types carry a behavioral contract
  (selection, result format, launcher); (3) custom types. Drivers **execute in Go**
  (`os/exec`, capture, normalize) — they never emit a command for an outer shell.
- **ED-1536** — launcher is a **declared** level-2 variant (`pytest/uv`), not detected.
  Bare `pytest` stays as a "resolve-for-me" convenience with a documented fallback
  precedence. Launcher is NOT an isolation tier.
- **ED-1537** (naming) — only the grammar SUBSET needed here: `/` splits family from
  variant (`pytest/uv`). The full `vnd.*` tree, curated bare-name registry, and public
  registry repo are **E-1793**, not this task.

## Scope boundary

- **In:** the `RunnerDriver` interface, the in-code driver registry + `family/variant`
  name parsing, the gotest driver, the generic driver, the pytest driver family with the
  `pytest/uv` variant (+ bare-`pytest` convenience), the `pytest-json-report` dev dep, and
  rewiring `verifycmd` to execute via drivers.
- **Out — E-1792:** removing `RenderRunScript`'s pure-`sh` emission
  (`internal/verify/runscript.go`) and updating E-1596's "triple-runnable" framing. This
  task must **preserve** `RenderRunScript`'s current behavior (see deliverable 6).
- **Out — E-1793:** the `vnd.*` naming tree, the curated registry, the public spec / `tbv`
  extraction.
- Do **not** change the Level-1 grammar: the `Check`/`Manifest` TOML fields
  (`runner`/`tests`/`paths`/`command`/`format`) and the two-level project+task config are
  unchanged. Only dispatch (runner string → driver) and execution change.

## Deliverables

### 1. `RunnerDriver` interface (`internal/verify/driver.go`)
Define an interface a driver implements to own its level-2 contract and execute a check:
- identity: the registered type it serves (family + optional variant) and its result
  `Format`.
- `Run(check Check, root dt.DirPath, env []string, reportPath dt.Filepath) (*Report, error)` —
  selects (translates `tests`/`paths` to the native filter), invokes via `os/exec` under
  `root`+`env`, captures the native stream (stdout or the report file), and normalizes to
  the CTRF-subset `*Report` (reusing `verify.Normalize`). This is the single execution path.
- `SelectionKind` (does the contract accept structured `tests`/`paths`, or only a raw
  `command`) so validation can enforce the two-form rules per driver instead of the current
  hardcoded first-class/raw split.

### 2. Driver registry + name parsing (`internal/verify/driver.go`)
- Replace `firstClassRunners`/`lookupFirstClass` with a registry keyed by registered type
  name; `LookupDriver(runner string) (RunnerDriver, ok)`.
- Parse `runner` into `family` and optional `variant` on a single `/` (ED-1537 subset);
  reject a malformed/empty family. A runner naming no registered type resolves to the
  **generic driver** (deliverable 4), preserving today's raw-command behavior.

### 3. gotest driver (`internal/verify/driver_gotest.go`)
- Port `translateGotest`/`captureGotest`/`goTestCmd`: selection → `go test -run '^(…)$'`
  (+ `-json`), paths default `./...`, format `gotest-json`, stream = stdout. Executes and
  normalizes in-driver.

### 4. generic driver (`internal/verify/driver_generic.go`)
- Serves `shell` and any unregistered runner: execs the check's literal `command` under
  `root`+`env`, reads the declared `format` stream from stdout, normalizes. Requires a
  normalizable `format` (default `tap`); this is the low-fidelity admission path — keep it
  working but do not special-case it. Preserves the current `runner = "sh"` + `format = "tap"`
  behavior the committed E-1603 suite relies on.

### 5. pytest driver family + `pytest/uv` variant (`internal/verify/driver_pytest.go`)
- Family `pytest`, variants dispatched by the parsed `variant`.
- **`pytest/uv`**: invoke the uv project's pytest with `--json-report
  --json-report-file=<reportPath>`, stream = file, format `pytest-json`. The contract MUST
  be robust under the runner's HOME/XDG isolation. E-1605's investigation (in E-1605's
  `--text`) found `uv run` under a temp HOME can hit a cold uv cache, while the project
  venv's plain `pytest` executable (present after `uv sync`) is isolation-robust — resolve
  accordingly (e.g. the venv executable, or `uv run --no-sync`) and **validate under the
  isolated env**, not just a bare shell.
- **bare `pytest`** convenience: documented fallback precedence — project-venv plain
  `pytest` executable → `uv run pytest` → bare `pytest` on PATH. This is the only place
  detection survives, as the bare family's contract.
- Do **not** build `pytest/venv` / `pytest/poetry` now; the seam accommodates them as future
  variants (add when a suite needs them).
- Add `pytest-json-report` to `[dependency-groups] dev` in `pyproject.toml`; `uv sync`.

### 6. Wire execution through drivers (`internal/verifycmd/env.go`)
- In `runChecks`, resolve each check's driver via `LookupDriver` and call `driver.Run(...)`
  in place of `CaptureCommand` + `runShell` + `Normalize`. Keep the existing per-check
  report-file allocation, the non-zero-exit-with-no-failures guard (`ErrCheckFailedNoResults`),
  isolation, setup/teardown, and CTRF merge unchanged.
- **Preserve `RenderRunScript`** (`internal/verify/runscript.go`): its removal is E-1792.
  If the refactor removes the string-emission helpers it depends on (`ResolvedCommand`/the
  translate funcs), keep a minimal bare-clone-command derivation on the driver (or retain
  `ResolvedCommand`) so `RenderRunScript` and its test stay green. Flag this bridge in a
  comment as E-1792-removable.

### 7. Validation (`internal/verify/check.go`)
- Rework `validateCheck` to the driver model: a check's `runner` resolves to a registered
  driver or the generic driver; `tests`/`paths` are legal only when the resolved driver's
  `SelectionKind` supports selection, else `command` is required; `format` rules per driver
  (inferred for registered types, declared for generic). Preserve the existing error
  sentinels' meaning where they still apply; add sentinels for an unknown/ malformed type.

## Files
- New: `internal/verify/driver.go`, `driver_gotest.go`, `driver_generic.go`,
  `driver_pytest.go` (+ `_test.go` each).
- Edit: `internal/verify/check.go` (drop `firstClassRunner*`, rework validation),
  `internal/verifycmd/env.go` (`runChecks` → driver execution), `internal/verify/runscript.go`
  (keep working via the bridge), `pyproject.toml` (dev dep).
- Reuse: `internal/verify/normalize.go` (`Normalize`), `ctrf.go` (`Report`/`MergeReports`),
  `internal/verifycmd/env.go` isolation/setup/teardown.

## Verify — `tests/tasks/e-1791-verify.sh` (E-1577 style; esu header; exit 0/1/2)
- Go unit tests for the driver seam, name parsing, gotest/generic/pytest drivers pass.
- A `pytest/uv` check executes a real pytest and normalizes its json-report to CTRF (the
  `pytest-json-report` plugin is installed) — under the runner's isolated env.
- Existing `runner = "gotest"` and `runner = "sh"`(tap) checks still normalize and merge
  (regression: the seam change must not alter their behavior).
- `RenderRunScript` still emits a bare-clone script and its test passes (E-1792 removes it).

## Regression
Run before handoff: `just test-go` (full `internal/...`), `just test` (Python),
`go build ./...`, `go vet ./...`, `gofmt -l`. The `internal/verify` +
`internal/verifycmd` unit suites are the primary gate (E-1605's exemplar suites re-verify
the seam after E-1605 rebases onto this).
