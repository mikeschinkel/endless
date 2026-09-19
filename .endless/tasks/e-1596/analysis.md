# Verification-suite system — design analysis

Per ED-1509, this epic's `analysis` holds the distilled design (vision, locked decisions,
interface contracts); the forward roadmap is in the epic's plan (`--text`); the full research is
in E-1600's `outcome`.

## Vision

A product feature of Endless: every task that reaches `verify` carries a committed, executable
**verification suite** — the complete test set (unit + end-to-end) proving that task's
acceptance criteria. Runnable three ways: (a) a bare `git clone` with Endless NOT installed;
(b) `endless verify <id>` on the user's behalf; (c) an AI agent so the user needn't run it.

## Terminology

- **verification suite** — the committed, executable proof of a task's acceptance criteria
  (unit + e2e). (Avoids "artifact," which in CI/CD means a build output.)
- **test runner** — the execution engine that runs the suite (`go test`, `pytest`, `bats`,
  testscript). Endless is never the test runner; it discovers suites, invokes the runner,
  isolates the run, normalizes results, presents pass/fail. (Avoids "runtime.")

## Load-bearing constraint

Endless must require NO external tool to use the core feature. Requiring mise/Docker/etc. would
itself be a reason not to adopt Endless. Heavier tooling must never become a precondition; the
design only must not block adding it later.

## Design principles

1. Triple-runnable (bare clone with no Endless / by Endless / by an agent).
2. Discoverer/orchestrator, never the test runner — convention + manifest, not a dependency.
3. Native runners underneath + a uniform discovery/result contract on top.
4. The suite is ALL testing for the task's scope (unit + e2e); may be slow when needed. Tiering
   = when you run (smoke vs full), not what's included.
5. CLI-only is a PRO — lets Endless drive tools invisibly.
6. All-Go future: no Python/uv lock-in.
7. Not a CI/CD platform. Hermetic builds / production parity are user-owned.
8. The chosen form must be trivially authorable by an AI agent with minimal-to-zero user input.

## Locked decisions (2026-06-20, post E-1600 research)

1. **Result contract = CTRF schema, subset implementation, vendored writer.** Implement only the
   CTRF fields we need, field-names/shapes matching CTRF, pinned to the CTRF snapshot
   (2025-11-24). Read native producers ourselves; NO runtime dependency on external CTRF
   reporters. (CTRF is pre-1.0/single-maintainer — we own the writer so upstream churn can't
   reach us; "CTRF-shaped" keeps later interop free.)
2. **Suite location = `.endless/tasks/<id>/`** — a product-controlled directory, so the
   convention is identical across every project regardless of its `tests/` layout.
3. **Scope = Stages 1–2 now; defer Stage 3 (containers) and Stage 4 (PTY/tmux).** Dogfood on
   Endless (the easy Tier-0 case) first.
4. **Build inside Endless, keep the seam clean** for possible later extraction; no extraction
   cost now beyond clean boundaries.

Note (general): the **product** Tier-0 substrate is a fresh temp working dir + isolated env
(temp `HOME`/`XDG_CONFIG_HOME`) — zero-dependency and app-agnostic. The temp-SQLite "fresh task
DB" is NOT part of the product runner; it is incidental to Endless being the system-under-test
(Endless's DB lives under XDG, so XDG isolation supplies it automatically). Tier 0 fits any
project whose state is contained by a temp dir; arbitrary projects with service dependencies
default to Tier 3 (containers), selected by the suite's declared `needs`. This does not violate
principle 2 — the core never *requires* a tool; `needs` selects the substrate a suite asks for.

## Interface Contract (foundation trio E-1602/E-1603/E-1604 MUST conform)

The three foundation children define interlocking interfaces. They are pinned here so the
children cannot diverge.

### A. The verify manifest — two-level config (E-1602 landed the per-task schema)

Two layers, merged at discovery (mirrors Endless's layered config / go-cfgstore):

- **Project-level `.endless/verify.toml`** — shared `setup`, default `needs`/`format`, shared
  fixtures. (Added by the setup/config-layering follow-up task.)
- **Per-task `.endless/tasks/<id>/verify.toml`** — the `runner`, task-specific `seed`,
  overrides. Beside the suite files and an optional `fixtures/`.

Discovery: glob `.endless/tasks/*/verify.toml`; merge with the project file. TOML (comments; no
YAML coercion footguns; Go-idiomatic). Kept flat by design.

```toml
schema = 1
task   = "E-1234"

[[check]]                              # first-class runner (B): structured tests
runner = "gotest"
tests  = ["TestFoo", "TestBar"]        # → go test -run 'TestFoo|TestBar' ./...

[[check]]
runner = "pytest"
tests  = ["tests/test_x.py::test_a"]

[[check]]                              # non-first-class (A): raw command fallback
runner  = "bats"
command = "bats ./.endless/tasks/E-1234/cli.bats"
format  = "tap"

setup  = ["just build"]               # top-level: prepare the project (cmds or script refs)
tiers  = ["smoke", "full"]            # optional
seed   = ["fixtures/baseline.json"]   # optional (E-1606)
needs  = []                           # optional; selects isolation tier (Stage 3+)
```

A verification is a **list of `[[check]]` entries** (A+B hybrid). First-class runners
(`gotest`, `pytest` to start) use a structured `tests` list Endless translates to the native
filter; any other runner uses a raw `command` + declared `format` (default `tap`). Rule: `tests`
is legal only on a first-class runner; otherwise require `command`. `setup`/`seed`/`needs`/`tiers`
stay top-level. This is the meta-test/selection concept: one ticket composes checks that SELECT
across the project's own runner set (reference); a snapshot is just a copied test file run by a
`command` check. The manifest is a POINTER, not a spec — it names runners and selects tests,
never re-describes them (no Gherkin-style spec layer).

**Three precondition kinds** (do not conflate): `needs` = provision the substrate (what must
exist); `setup` = prepare the project (build/install/migrate/codegen); `seed` = load
state/fixtures. Standard script homes: `.endless/verify/` (project-shared, e.g.
`.endless/verify/setup.sh`) and each task's `.endless/tasks/<id>/` dir; short steps may inline.

### B. Runner → result dataflow (E-1603 produces, E-1604 consumes)

1. E-1603 reads the manifest and creates **generic, app-agnostic per-run isolation**: a fresh
   temp working dir + an isolated env (temp `HOME`, temp `XDG_CONFIG_HOME`) so the suite can't
   touch the developer's real home/config (Tier 0). The runner has NO application-specific state
   knowledge (no task DB, no SQLite). *Self_dev note:* since Endless's own DB lives under
   `XDG_CONFIG_HOME`, isolating XDG gives an Endless-SUT suite a fresh DB for free — incidental,
   not runner logic.
2. E-1603 runs preconditions in order: provision (Tier-0 no-op) → merged `setup` steps (abort
   loudly on failure) → `seed` (E-1606).
3. For each `[[check]]`, E-1603 runs it (first-class: translate `tests` → native filter; raw:
   run `command`), capturing exit code + the native result stream named by the check's `format`.
4. E-1603 passes each (raw native stream, `format`) to E-1604's normalizer; E-1604 normalizes
   each to CTRF and **merges all checks into one report**.
5. E-1603 tears down the temp dir (honor a `--keep` debug flag).

### C. The CTRF-subset envelope (E-1604 owns — the stable output contract)

A single JSON document (UTF-8). Subset we implement now:

- Top: `results.tool.name`; `results.summary { tests, passed, failed, skipped, pending, other,
  start, stop }`; `results.tests[]`.
- Per test: required `name`, `status` (`passed|failed|skipped|pending|other`), `duration` (ms);
  optional `message`, `trace`, `suite`, `stdout`, `stderr`, `attachments[]`
  (`{ name, contentType, path }` — the non-text hook: screenshots/HTTP transcripts), `extra`.
- Field names/shapes match CTRF; emit only this subset; ignore the rest until needed. Endless
  WRITES this itself from the native stream; no external CTRF reporter at runtime.

### D. Exit-code contract (bare-clone mode)

The `runner` string alone must exit 0 on all-pass and non-zero on any failure, with no Endless
present. CTRF normalization and seeding are Endless-only enhancements unavailable in bare mode;
the runner string must be self-sufficient.

## Lessons from ad-hoc prototypes

`tests/tasks/e-1577-verify.sh` and `tests/tasks/e-1570-verify.sh` are reference-only prototypes
(not deliverables). Lessons, now folded into the design: (a) the sandbox lacked a supported
seeding verb (E-1570's author reached into `endless-go session-query record-bg-agent`) → E-1606
adds first-class seeding; (b) two happy paths were left MANUAL because they `exec` into
`claude attach` or need a live tmux → Stage 4 (deferred) automates these via PTY/disposable
container; (c) Click prints `No such option: --type` with a capital `N`, so the E-1577
prototype's lowercase `"no such option"` pattern silently false-negatives on flag-removal
checks (a "this option was removed" assertion that never actually matches) → the shared
"option rejected" assertion helper must match the runner's real output case-accurately, or
normalize case before comparing. Surfaced building `tests/tasks/e-1540-verify.sh`.
