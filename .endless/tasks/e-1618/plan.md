# Plan — E-1618: revise verify.toml to a `[[check]]` list

## Context

E-1602 landed a per-task `verify.toml` with a **single** `runner` + `format` (struct, parser,
discovery glob). E-1611 then added `setup` and a project-level `ProjectConfig`/`Merge` layer.
The epic (E-1596) interface contract — section A — has since been revised to an **A+B hybrid**:
a verification is a **list of `[[check]]` entries**, so one ticket composes multiple runners
into one proof. This task (cleans up E-1602/E-1611) brings the Go `internal/verify` package in
line with that contract.

Two check forms:

- **First-class runner (B)** — `runner` is a known runner (`gotest`, `pytest`). Endless infers
  the result `format` from the runner and translates a structured `tests`/`paths` selection into
  the native filter command. An explicit `command` is also accepted as an escape hatch.
- **Raw command (A, fallback)** — any other runner uses a literal `command` + a declared
  `format` (default `tap`). The escape hatch for runners Endless doesn't translate.

Blast radius is small: **no code outside `internal/verify` references the schema**, and **no
`verify.toml` files exist in the repo yet**, so there's no data migration — only the package and
its tests change. Execution of checks (E-1603) and CTRF normalization + merge (E-1604) are
downstream; E-1604 #5's translator note explicitly defers to "the `[[check]]` schema-revision
task" — i.e. **E-1618 owns the `tests`→native-filter translators**; E-1604 keeps normalize+merge.

### Decisions locked with Mike (2026-06-23)

1. **Remove `ProjectConfig.Format`** — format is now per-check (inferred for first-class, declared
   /`tap`-default for raw). A single project-wide default is meaningless when one manifest mixes
   `gotest`+`pytest`+`bats`. `setup`/`seed`/`needs`/`teardown` still merge from project config.
2. **Add a top-level `teardown` field** (symmetric with `setup`). Generated `run.sh` =
   `setup` → checks → `teardown`, with a shell trap so teardown always runs. `seed`/`needs`
   stay Endless-only (excluded from `run.sh`).
3. **First-class checks: allow `command`/`format` if consistent.** A first-class runner check may
   carry an explicit `command` (escape hatch) or an explicit `format` — but the `format`, if
   given, must equal the runner's inferred format (loud error on mismatch). `command` and
   `tests`/`paths` are mutually exclusive on a first-class check.
4. **Add a per-check `paths` field** for structured test scoping (e.g. Go package patterns)
   alongside `tests` names — not just bare test names. (Mike chose the structured field over a
   command-only escape hatch.)

## The revised schema

```toml
schema = 1
task   = "E-1234"

setup    = ["just build"]          # top-level, ordered; run before checks
teardown = ["docker compose down"] # top-level, ordered; run after checks (always, via trap)
tiers    = ["smoke", "full"]       # optional
seed     = ["fixtures/baseline.json"] # optional (E-1606); Endless-only
needs    = []                      # optional; selects isolation tier (Stage 3+)

[[check]]                          # first-class: structured selection -> native filter
runner = "gotest"
tests  = ["TestFoo", "TestBar"]    # -> go test -run '^(TestFoo|TestBar)$' ./...
paths  = ["./internal/verify/..."] # optional; scopes packages (default ./...)

[[check]]
runner = "pytest"
tests  = ["tests/test_x.py::test_a"]

[[check]]                          # raw fallback: literal command + declared format
runner  = "bats"
command = "bats ./.endless/tasks/E-1234/cli.bats"
format  = "tap"
```

**TOML ordering constraint (important):** top-level scalars/arrays (`schema`, `task`, `setup`,
`teardown`, `tiers`, `seed`, `needs`) MUST appear **before** any `[[check]]` block — in TOML a
bare `key = val` after `[[check]]` binds to that check table, not the document root. The epic
analysis's illustrative example puts them after; the real files (and all test fixtures) order
them first. A top-level key misplaced under `[[check]]` is caught loudly by the existing
unknown-key (`Undecoded`) rejection.

## Deliver

### 1. `Check` struct + first-class runner registry — new `internal/verify/check.go`

```go
type Check struct {
    Runner  string   `toml:"runner"`
    Tests   []string `toml:"tests"`
    Paths   []string `toml:"paths"`
    Command string   `toml:"command"`
    Format  Format   `toml:"format"`
}

// firstClassRunner: name -> inferred format + (tests,paths) -> native command.
// Adding a runner later = add one entry to firstClassRunners.
type firstClassRunner struct {
    name      string
    format    Format
    translate func(tests, paths []string) string
}

var firstClassRunners = []firstClassRunner{
    {name: "gotest", format: FormatGotestJSON, translate: translateGotest},
    {name: "pytest", format: FormatPytestJSON, translate: translatePytest},
}
```

- `translateGotest(tests, paths)` → `go test [-run '^(<tests|>)$'] <paths or ./...>`. Names are
  anchored `^(...)$` (exact match — bare `TestFoo` would otherwise also match `TestFooBar`);
  this refines the plan's illustrative un-anchored example. `paths` default `["./..."]`.
- `translatePytest(tests, paths)` → `pytest <paths...> <tests...>` (pytest positional args accept
  both dirs/files and `path::test` nodeids).
- Lookup helpers: `lookupFirstClass(name) (firstClassRunner, bool)` and `firstClassNames()`
  (comma-joined, for error metadata) — mirrors the existing `knownFormats`/`formatList` pattern.
- `Check` methods: `IsFirstClass()`, `ResolvedFormat()` (inferred for first-class, declared-or-
  `tap` for raw), `ResolvedCommand()` (translated, or the literal `Command`).

### 2. Revise `Manifest` + validation — `internal/verify/manifest.go`

- Drop `Runner`/`Format` from `Manifest`; add `Checks []Check` (`toml:"check"`) and
  `Teardown []string` (`toml:"teardown"`). Keep `Schema`/`Task`/`Setup`/`Tiers`/`Seed`/`Needs`.
- Keep the `Format` type, its constants, `Valid`, `knownFormats`, `formatList` — still used
  per-check and by the E-1604 normalizer.
- `Manifest.Validate`: schema + task as today; **`Checks` must be non-empty**; then per-check
  `validateCheck` for each entry:
  - must set `runner`;
  - **first-class runner:** `command` XOR structured (`tests`/`paths`). In command mode, `tests`
    and `paths` must be empty. In structured mode, at least one of `tests`/`paths` non-empty.
    Explicit `format`, if set, must equal the inferred format (else loud mismatch error).
  - **non-first-class runner:** `command` required; `tests` and `paths` must be empty (`tests`
    legal only on a first-class runner); `format` optional, default `tap`, validated if set.
- `ParseManifest`/`decodeManifest`/`LoadManifest` stay (standalone bare-clone path); unknown-key
  rejection unchanged. Update the package doc comment (manifest is now a list of check pointers).

### 3. Project config + merge — `project.go`, `merge.go`

- `project.go`: remove `Format` field and its validation branch. `ProjectConfig` keeps
  `Schema`/`Setup`/`Seed`/`Needs`; add `Teardown []string`. `format` on a project config now
  falls through to the unknown-key rejection (loud).
- `merge.go`: remove the format-default block; add `Teardown` to the project-first list-append
  (same direction as `setup`, for v1 simplicity — note LIFO/reverse-order as a possible future
  refinement, deferred since teardown has no live consumer until E-1603). Update the doc comment.

### 4. Bare-clone emit — new `internal/verify/runscript.go`

- `RenderRunScript(m *Manifest) string` → a POSIX `sh` script that runs the verification with no
  Endless present, by exit code alone:
  - header comment ("Generated by Endless from .endless/tasks/<task>/verify.toml — DO NOT EDIT");
  - `set -eu`; a `teardown()` function + `trap teardown EXIT` so teardown always runs;
  - the merged `setup` commands, then each check's `ResolvedCommand()` in order;
  - excludes `seed`/`needs` (Endless-only per contract D).
- This is the pure renderer (unit-testable). Wiring it into a CLI surface / writing the file to
  disk is E-1603's (the runner's) concern, not this task.

### 5. Errors — `errors.go`

Add focused sentinels (reusing `ErrMissingField` where natural), e.g.: `ErrNoChecks`,
`ErrTestsRequireFirstClass`, `ErrPathsRequireFirstClass`, `ErrRawCheckNeedsCommand`,
`ErrFirstClassNeedsSelection`, `ErrFirstClassCommandConflict`, `ErrFormatMismatch`. Each wraps
`ErrInvalidManifest`; messages carry the runner name + accepted first-class names for diagnosis.

### 6. Ad-hoc per-task verify script — new `tests/tasks/e-1618-verify.sh`

Modeled on the established `tests/tasks/e-1602-verify.sh` (the direct predecessor this task
supersedes): same `bash`, `set -u`, color-aware `section`/`report_pass`/`report_fail`/`summary`
helpers, `assert_succeeds`/`assert_fails_with`/`assert_contains`/`assert_not_contains`, ALL-PASSED
summary, exit 0 all-pass / 1 any-fail / 2 env-problem. This realizes the E-1596 vision (a
committed, runnable per-task proof) ad-hoc, while the formal system is built by the other session.
Sections:

1. **Static analysis** — `internal/verify` builds, vet-clean, gofmt-clean; whole module compiles.
2. **Unit tests** — runs the package suite by acceptance criterion (`TestParseManifest`,
   `TestCheck*`/`TestTranslate*`, `TestRenderRunScript`, `TestMerge`, `TestDiscover`).
3. **Behavioral proof** — a throwaway Go harness (dot-prefixed temp dir under the module, cleaned
   on exit, same trick as e-1602) that calls `verify.Discover` against real on-disk
   `.endless/tasks/` fixtures using the **new `[[check]]` schema**, and prints/asserts the
   observable results: a mixed gotest+pytest+bats manifest discovers; `tests` on a raw runner
   fails loudly; raw runner without `command` fails loudly; first-class translation produces the
   expected anchored `go test -run '^(...)$' <paths>` / `pytest <nodeids>` command; the harness
   also prints `RenderRunScript` output and asserts setup → checks → teardown ordering with the
   trap and no seed/needs lines.

### 7. Reconcile superseded predecessor scripts — `e-1602-verify.sh`, `e-1611-verify.sh`

The schema change breaks both predecessor scripts (their Go harness reads `m.Runner`; their
fixtures use top-level `runner`/`format`). Since E-1618 **cleans up E-1602/E-1611**, update both
minimally to the revised `[[check]]` schema so the `tests/tasks/` suite stays green (harness uses
`m.Checks`/`ResolvedCommand`; fixtures use `[[check]]`; drop the project-`format` assertions in
e-1611, add a `teardown` merge assertion). They keep their distinct scope (e-1602: schema +
discovery; e-1611: setup + project-config merge); e-1618 adds the check-list + translation +
run.sh coverage. (If Mike prefers, the two can instead be deleted as fully superseded by
e-1618-verify.sh — default here is non-destructive update.)

### 8. Tests

Rewrite the four existing test files for the new schema and add two:

- `manifest_test.go` — valid full (mixed gotest+pytest+bats) / minimal; missing schema/task;
  empty `Checks`; per-check validation matrix (tests on raw runner fails; raw without command
  fails; first-class with both tests+command fails; first-class with mismatched format fails;
  first-class with consistent format passes); unknown-key + malformed TOML still rejected;
  **TOML ordering** (top-level keys before `[[check]]`).
- `check_test.go` (new) — registry lookup; `translateGotest` anchoring + `paths` scoping +
  default `./...`; `translatePytest` nodeid/paths joining; `ResolvedCommand`/`ResolvedFormat`
  for first-class (structured + command mode) and raw.
- `runscript_test.go` (new) — `RenderRunScript` emits setup → resolved checks → teardown, with
  the trap; excludes seed/needs; concrete commands only.
- `merge_test.go` — drop the `Format` cases; rebuild `taskManifest()` around `Checks`; add a
  `Teardown` project-first append case.
- `project_test.go` — drop the `format` field cases; assert `format` is now rejected as an
  unknown key; add a `teardown` accept case.
- `discover_test.go` — update fixtures to the `[[check]]` schema; keep glob/merge/task-id-
  mismatch coverage.

## Critical files

- `internal/verify/manifest.go` — `Manifest` struct, `Validate` + `validateCheck`, parse path.
- `internal/verify/check.go` (new) — `Check`, registry, translators, resolved methods.
- `internal/verify/runscript.go` (new) — `RenderRunScript`.
- `internal/verify/project.go`, `merge.go` — drop `Format`, add `Teardown`.
- `internal/verify/errors.go` — new sentinels.
- `internal/verify/{manifest,check,runscript,merge,project,discover}_test.go`.
- `tests/tasks/e-1618-verify.sh` (new) — ad-hoc per-task proof.
- `tests/tasks/e-1602-verify.sh`, `tests/tasks/e-1611-verify.sh` — update to revised schema.

## Verification

End-to-end, from the worktree (`/Users/mikeschinkel/Projects/endless/.endless/worktrees/e-1618`):

1. `just build` — Go binaries + codegen compile clean (catches any cross-package breakage; none
   expected since nothing outside the package uses the schema).
2. `go test ./internal/verify/...` — all package tests pass, exercising:
   - a mixed `gotest`+`pytest`+`bats` manifest parses and validates;
   - `tests` on a non-first-class runner fails; missing `command` on a raw runner fails;
   - first-class `tests`/`paths` translate to the expected anchored native commands;
   - `RenderRunScript` output runs every check by exit code with setup/teardown wrapped.
3. `go vet ./internal/verify/...` clean.
4. `./tests/tasks/e-1618-verify.sh` — the ad-hoc per-task proof prints ALL PASSED (exit 0),
   covering static analysis, the unit suite by criterion, and the behavioral `Discover` +
   `RenderRunScript` proof against on-disk `[[check]]` fixtures.
5. `./tests/tasks/e-1602-verify.sh` and `./tests/tasks/e-1611-verify.sh` — both updated
   predecessors still print ALL PASSED under the revised schema (no broken scripts left behind).

When green: `endless task update E-1618 --status verify --db main`, hand back the how-to-test and
the return line. Do not land/drop without Mike's go-ahead (the spawning session owns landing).
