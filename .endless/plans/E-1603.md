# Plan — `task verify` Tier-0 runner

Conform to the epic's interface contract (`endless task show E-1596 --analysis`,
sections "Interface Contract / B" and "/ D"); don't re-derive the dataflow.

## Deliver

`endless task verify <id>` (and `endless task verify` for the cwd's task), Tier 0 only:

1. Resolve the task's merged manifest via E-1602 discovery + E-1611 project/task config layering.
2. **Generic per-run isolation (product-level, app-agnostic):** create a fresh per-run temp
   working directory and run the suite under an isolated environment — a temp `HOME` and temp
   `XDG_CONFIG_HOME` — so the suite cannot read or pollute the developer's real home/config.
   This is the zero-dependency Tier-0 substrate; it knows NOTHING about any specific
   application's state.
3. **Run preconditions in order** before testing: provision (Tier-0 = no-op) → the merged
   `setup` steps (aborts loudly on a failing step). `seed` execution semantics belong to E-1606;
   until then a non-empty `seed` is refused loudly (as is a non-empty `needs`) — the runner never
   does less than the manifest declares.
4. For each `[[check]]`, run it instrumented to emit its native result stream: a first-class
   runner (gotest/pytest) is translated to its capture command (gotest gains `-json` on stdout;
   pytest writes the json-report plugin's file), and a raw check runs its `command`, emitting its
   declared `format`. Capture the stream + exit code. The Endless-mode capture command lives on
   the check beside the bare-clone `ResolvedCommand` (`CaptureCommand`), keeping runner-specific
   knowledge in the runner registry rather than the orchestrator.
5. Normalize each stream by its `format` and merge to one CTRF report (E-1604); print a pass/fail
   summary + the CTRF path. Exit 0 on all-pass, non-zero otherwise — including a check that exits
   non-zero while reporting no test failures (a build/runner error), which fails loudly.
6. Tear down the temp dir (running any `teardown` steps first, always); honor `--keep` to retain
   it for debugging (mirrors testscript `-testwork`).

## CLI surface

`endless task verify` is a subcommand of `task`: the object is a task (it proves that task's
acceptance criteria), and it sits beside `task confirm`/`assume` as the "prove it" step before
the "accept it" step. The Go plumbing is the internal `endless-go verify` subcommand; a thin
Python `task verify` wrapper resolves the task id (defaulting to the session's active task) and
execs the worktree-aware binary.

## Boundaries

- Tier 0 only. `needs`-driven escalation (Tiers 2–4) is later stages; a non-empty `needs` fails
  loudly with "tier not yet supported" rather than silently running unisolated.
- **PRODUCT vs self_dev — keep them separate.** The runner provides ONLY generic env +
  working-dir isolation. It must contain NO Endless-specific state handling — no "task DB", no
  SQLite knowledge. Application-specific preconditions are the suite's responsibility (E-1606
  seeding / fixtures, or the project's own setup).
- **Self_dev note (NOT product behavior):** because Endless stores its own DB under
  `XDG_CONFIG_HOME`, isolating XDG automatically gives an Endless-as-SUT suite a fresh DB for
  free — no special-casing in the runner.
- Concurrency-safety comes from each run's own temp working dir + isolated env (multiple-sandbox
  hardening for the self_dev case is E-1608).
- Depends on E-1602 (manifest/discovery), E-1604 (normalizer), and E-1611 (the `setup` field +
  merged project/task config this runner executes).

## Verify — ad-hoc verify script (interim, E-1577 style)

Committed script `tests/tasks/e-1603-verify.sh`, modeled on `tests/tasks/e-1577-verify.sh` (its
shared `assert_*` / `report_pass` / `report_fail` helpers, section headers, an ALL-PASSED
summary, exit 0 on all-pass / 1 on any failure). It builds the worktree binary, runs the Go unit
tests for the runner and its manifest/normalizer deps, then asserts:

- a passing suite → exit 0, CTRF written, summary shown.
- a deliberately failing suite → non-zero exit, failure detail surfaced.
- the suite runs with `HOME`/`XDG_CONFIG_HOME` pointed at temp dirs — confirm it cannot see the
  developer's real config.
- two concurrent runs don't collide.
- `--keep` retains the temp dir; default removes it.
- `needs` and `seed` fail loudly at the Tier-0 boundary.
- a first-class gotest check normalizes to CTRF via `-json` capture.

Interim location `tests/tasks/`; migrate to the formal `.endless/tasks/<id>/` convention once the
system lands (E-1623).
