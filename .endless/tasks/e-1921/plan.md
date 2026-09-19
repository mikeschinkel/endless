# Plan — one freshly built endless-go for every Python test

## The problem, measured

`just test` never builds. Python tests that run `endless-go` exercise whatever
binary they find, and in E-1917's worktree that was a copy of main's — producing
a bogus "tasks has 19 columns but 18 values" failure.

Measured before planning, by moving `bin/endless-go` aside and running the full
suite: 5 tests fail (all in `test_template_materialize.py`), 2 SKIP silently,
3456 pass. That is a FLOOR. A test that runs a STALE binary still passes, and a
stale binary is the E-1917 incident exactly.

The Python tests do not resolve the binary one way. They resolve it five ways,
and only one of them builds:

  1. `conftest.isolated_env` (autouse, so every test) prepends `<checkout>/bin`
     to PATH. Nothing builds what is there.
  2. `test_template_materialize._bin()` hard-codes `<checkout>/bin/endless-go`
     by absolute path. Bypasses PATH entirely; fails when absent.
  3. `test_land_conflict.ENDLESS_GO` hard-codes the same path, and `skipif`s
     when it is absent — silent skip when missing, stale when present.
  4. `test_guide_conditionals` PREFERS `bin/endless-go` whenever it exists, falls
     back to PATH, and builds only as a last resort — despite a docstring saying
     "a guard that silently skips … is not a guard".
  5. `test_default_branch_parity.endless_go` builds from this checkout. Correct,
     and the only one that is.

So a PATH-only fix would not have fixed the five tests exposed today: they
reach the binary by absolute path.

## Decisions (owner, 2026-09-17)

  - Research AND fix, in this task. Retyped research → todo.
  - Measure the real count, not just the floor (§2).
  - GLOBAL, not scoped to the tests §2 finds. E-1063 turns Python commands into
    shims over `endless-go` one at a time, so the set of tests that shell out
    GROWS through the port. A hand-maintained scope would need every newly
    shimmed command added, which is the forgettable-list failure again.
  - PRODUCT: no change. `verify.toml`'s `setup` is already where a project
    declares its own build, and Endless cannot know a downstream build graph.
  - Worth doing ahead of E-1063. E-2084 puts E-1921 first because "the port's
    whole safety net is 'the tests pass'".

## 1. One build site

A session-scoped fixture in `tests/conftest.py`, `endless_go_bin`, builds
`./cmd/endless-go` once into `tmp_path_factory` and returns the path. It is the
existing `test_default_branch_parity.endless_go` fixture, MOVED rather than
copied — reuse before creation.

  - Build failure fails the session loudly (`pytest.fail` with the compiler's
    stderr). It must never fall back to `bin/` or PATH: a fallback is the stale
    binary this task removes.
  - `go` missing from PATH fails with a message naming it.

## 2. Measure the real count first

Before changing any resolver, run the full suite once with every route to the
binary instrumented:

  - a logging `endless-go` shim FIRST on PATH, and
  - `<checkout>/bin/endless-go` temporarily replaced by the same shim (the real
    binary moved aside and restored by trap), since routes 2–4 bypass PATH.

The shim appends `$PYTEST_CURRENT_TEST` and its argv to a log, then execs the
real binary. pytest sets that variable per test and subprocesses inherit it, so
every invocation is attributed to the test that caused it.

The outcome records: the number of tests that invoke the binary, by route, and
any route not in the five above. If §2 finds a sixth route, §3 covers it.

## 3. Point every route at the one build

  - Route 1: `isolated_env` prepends `endless_go_bin`'s directory instead of
    `<checkout>/bin`. It stays autouse, so every test gets a current binary
    without anyone listing it — the global decision.
  - Routes 2, 3, 4: replace each test's own resolver with the fixture. Route 3's
    `skipif` is DELETED: a test that silently skips when the binary is missing
    proves nothing in the one environment where it matters.
  - Route 5: uses the fixture it donated.

Also corrected, because they now describe the wrong mechanism:
`isolated_env`'s comment (it names `endless-event` and a sibling-worktree
symlink) and `test_epic_cmd.py`'s docstring (it says event emission runs through
the binary on PATH; with the binary removed those tests pass).

Not touched: `just test-go`. Go's `TestMain` in `eventcmd` and `sessionquerycmd`
already builds per package and is correct by construction.

## 4. How this ends — E-1063

The fixture carries no weight past the port and needs no migration. E-2084
settled that "the suite the port leaves behind is Go's" and lists the pytest
isolation fixtures as subsumed; this fixture is deleted with `tests/`. The one
rule a ported test must keep is Go's existing one: use the `TestMain`-built
binary, never `bin/endless-go`.

## 5. Costs, measured

  - One build per pytest SESSION: 1.4 s with a warm Go cache, 8.1 s cold.
  - The verify runner substitutes HOME, so GOCACHE starts cold there. Each
    `uv run pytest` gate in a suite is its own session and pays ~8 s.
  - A Go compile error now fails the Python suite too. Deliberate: testing
    Python against a binary that no longer matches its source is the bug.

No mitigation for the cold cache is in scope.

## 6. Verification

`.endless/tasks/e-1921/verify.sh`, sourcing `_harness.sh`:

  1. Fail-fast gate: the tests this task changes, plus the full Python suite.
  2. THE INCIDENT, inverted into a test. Replace `<checkout>/bin/endless-go`
     with a POISONED binary (a script that exits 99), and put another poisoned
     `endless-go` first on PATH. Run the previously exposed files —
     `test_template_materialize.py`, `test_land_conflict.py`,
     `test_guide_conditionals.py` — and require them to PASS with zero skips.
     Restore by trap. That single run is the whole claim: neither `bin/` nor
     PATH can influence which binary a test runs.
  3. Remove `<checkout>/bin/endless-go` entirely and require the same files to
     pass with zero skips (the silent-skip route is gone).
  4. Structure: no test file resolves `bin/endless-go` itself, and the only
     `go build` of `./cmd/endless-go` under `tests/` is `endless_go_bin`.
  5. The count from §2 is recorded in the outcome, and the suite asserts the
     instrumented run found no route outside those the fixture covers.
