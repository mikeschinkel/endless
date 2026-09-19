# Plan — E-1605: testscript/txtar + pytest forms + first dogfood suites

## Context

The E-1596 epic builds Endless's per-task verification-suite system. Its foundation
trio has landed: E-1602 (manifest schema + discovery), E-1603 (the Tier-0
`endless-go verify` runner), E-1604 (CTRF-subset writer + `gotest-json`/`pytest-json`/`tap`
normalizers + the first-class `gotest`/`pytest` shortcut registry). Two runner *forms*
the system still lacks in practice: an end-to-end **txtar/testscript** form for Go CLIs,
and a working **pytest** form. This task adds both and proves the whole stack with the
first two real `.endless/tasks/<id>/verify.toml` suites (converting the interim
`tests/tasks/e-16{03,58}-verify.sh` bash scripts into product suites). Do **not** retrofit
any of the other interim `tests/tasks/*-verify.sh` scripts in this task — only the two named
exemplars; the broader interim-script cleanup is a separate concern.

A design fork surfaced during planning and was resolved with Mike: the first-class
`pytest` shortcut (our own code, `internal/verify/check.go`) emits a bare `pytest …`
command, but this project is uv-managed and `pytest` isn't on PATH. Decision: **make it
just work** by resolving the Python test launcher through an **adapter** rather than
layering tool-specific conditionals into the generic runner. The adapter prefers
`.venv/bin/pytest` (a plain executable — no uv, no network, survives the runner's
temp-HOME isolation), then `uv run pytest`, then bare `pytest`. Other Python tools
(poetry/pdm) plug in as future adapters; the raw-command path covers them meanwhile.
**Status: complete.** The launcher-resolution design went through the brainstorm **E-1789**
(ED-1535/1536/1537) and its implementation **E-1791**, which landed the `RunnerDriver`
interface + the `pytest/uv` driver (launcher precedence `.venv/bin/pytest` → `uv run
--no-sync pytest` → bare `pytest`). E-1605 rebased onto that and finished the pytest slice:
the E-1603 suite's `pytest/uv` check + `tests/test_verify_cmd.py`. §2 below is retained as
the original design record; the shipped mechanism is E-1791's driver, not an in-task adapter.

## Key facts established

- Runner executes each `[[check]]` via `sh -c` with cwd = the **project root discovered
  from cwd** (`findProjectRoot` walks up to the first `.endless/`) — the **worktree** in a
  self-dev session, not main; isolation replaces only `HOME`/`XDG_CONFIG_HOME` (PATH
  preserved), no temp working dir: `internal/verifycmd/env.go`.
- First-class registry + command emission: `internal/verify/check.go`
  (`firstClassRunners`, `translateGotest`/`captureGotest`, `translatePytest`/`capturePytest`,
  `Check.ResolvedCommand()`, `Check.CaptureCommand(reportPath)`).
- Only two emission call sites: `internal/verify/runscript.go:54` (bare-clone) and
  `internal/verifycmd/env.go:142` (capture).
- Formats supported: `gotest-json`, `pytest-json`, `tap` (`internal/verify/normalize.go`).
  A txtar suite needs **no new format** — testscript runs under `go test`, captured as `gotest`.
- SUT for the E-1758 exemplar: `monitor.WorktreeAnomaliesAt(projectRoot, worktreePath)`
  (`internal/monitor/worktree_anomalies.go`), reached via the CLI
  `endless-go session-query worktree-anomalies --worktree-path X --project-root Y`
  (`internal/sessionquerycmd/session_query.go:runWorktreeAnomalies`). Unit tests:
  `TestWorktreeAnomaliesAt`, `TestUserStatusPaths`, `TestIsAutoManagedPath`,
  `TestWorktreeAnomalyLine`.
- Go skips dot-dirs in package patterns ⇒ txtar Go harness **cannot** live under
  `.endless/…`; it lives in a conventional Go location, the manifest merely *points* at it.
- `github.com/rogpeppe/go-internal` is not yet in go.mod (present only in go.work.sum);
  pure module mode, no `vendor/` tree.

## 1. testscript / `.txtar` form (Go CLI/e2e)

- Add `github.com/rogpeppe/go-internal` as a go.mod dependency (module mode); `go mod tidy`.
- **Harness** (co-located with the SUT): `internal/monitor/worktreecheck_scripts_test.go` —
  `TestWorktreeCheckScripts(t)` calling `testscript.Run(t, testscript.Params{Dir: "testdata/worktreecheck", Setup: …})`.
  - In `TestMain` (or Setup), build `endless-go` once into a temp dir via
    `go build -o <tmp>/endless-go ./cmd/endless-go` and prepend that dir to the script env's
    PATH so `.txtar` scripts invoke `endless-go …`. `git` is already on PATH. Prefer an
    existing `bin/endless-go`/`$ENDLESS_GO_BIN` if present (fast), else build (bare-clone).
- **`.txtar` files**: `internal/monitor/testdata/worktreecheck/*.txtar`, e.g.
  `clean.txtar` (git repo, no anomalies → empty stdout, exit 0), `uncommitted.txtar`
  (stray user file → `uncommitted: …`), `branch-mismatch.txtar` (companion branch ≠ HEAD →
  `branch-mismatch: …`). Each `exec git init/add/commit`, writes a companion where needed,
  runs `endless-go session-query worktree-anomalies …`, asserts stdout/exit.
- Document the convention (package doc comment on the harness): where `.txtar` files live and
  how the harness builds/finds the SUT binary.

## 2. pytest form + Python-tool adapter — DEFERRED (blocked by E-1789)

**Not built in this session.** The first-class pytest launcher must resolve through an
adapter (not inline conditionals), and the durable driver/adapter architecture is being
designed under the blocking brainstorm **E-1789**. E-1605 keeps this deliverable and stays
blocked_by E-1789; the pytest slice is implemented against the agreed seam in a later
session. Deferred here: `pytest-json-report` dev dep, the driver/adapter refactor of
`internal/verify`'s first-class registry, project-root threading through
`Check.ResolvedCommand`/`CaptureCommand`, the `tests/test_verify_cmd.py` module, and the
E-1603 suite's pytest check. Design context lives in E-1789's `--analysis`.

## 3. First reference suites (multi-`[[check]]`, merge → one CTRF)

- **`.endless/tasks/E-1758/verify.toml`** (txtar + gotest):
  - gotest check — unit tests: `tests = [TestWorktreeAnomaliesAt, TestUserStatusPaths, TestIsAutoManagedPath, TestWorktreeAnomalyLine]`, `paths = ["./internal/monitor/..."]`.
  - gotest check — txtar harness: `tests = [TestWorktreeCheckScripts]`, `paths = ["./internal/monitor/..."]`.
- **`.endless/tasks/E-1603/verify.toml`** (gotest + raw now; pytest check added later under E-1789):
  - gotest check over `internal/verify` + `internal/verifycmd` (the runner's unit tests).
  - raw check: `runner = "sh"`, `command = ".endless/tasks/E-1603/e2e.sh"`, `format = "tap"` —
    a committed script driving `endless-go verify` against an inline throwaway fixture suite,
    emitting TAP (ports the end-to-end assertions from the interim `e-1603-verify.sh`).
  - the first-class **pytest** check is deferred (see §2 / E-1789); it slots into this same
    manifest when the adapter lands.

## 4. Verify script — `tests/tasks/e-1605-verify.sh` (E-1577 style; esu header; exit 0/1/2)

Fold this task's own tests into one fail-fast suite. Builds `bin/endless-go`. Asserts
(the unblocked slice only — no pytest yet):
- `endless-go verify E-1758` → exit 0, CTRF written, its txtar + gotest checks ran.
- `endless-go verify E-1603` → exit 0, CTRF written, its gotest + raw checks ran.
- the txtar harness passes under plain `go test` with no Endless present (bare-clone).
- `go mod tidy` leaves go.mod/go.sum clean; the imported testscript dep builds.
The pytest-check assertions are added with the pytest slice (E-1789).

## House rules

Apply the Go house-rules skills (ClearPath, doterr, go-dt) to all Go changes; shell-script
skill for the verify script and `e2e.sh`. Reuse existing helpers
(`internal/verifycmd` `displayPath`/`tail`, the `e-1577-verify.sh` `assert_*`/`report_*`
skeleton). No new format, no new runner subcommand.

## Verification

One command for Mike: `esu && ./tests/tasks/e-1605-verify.sh` (exit 0 = all pass).
Project-wide regression I run myself before handoff: `just test-go`, `just test`,
`go build ./...`, `go vet ./...`, `gofmt -l`.
