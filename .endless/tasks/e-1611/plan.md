# Plan — setup field + project-level verify config layering

Adds environment **preparation** to the verify manifest — the gap E-1602 (landed) didn't cover.
Shared design is in the epic's analysis (`endless task show E-1596 --analysis`, Interface
Contract). Conform to it.

## The three precondition kinds (taxonomy)

- `needs` → **provision the substrate** (what must exist: toolchain, services). Already in the
  manifest; satisfied by the isolation tier.
- **`setup`** → **prepare the project** (build / install / migrate / codegen). THIS task adds it.
- `seed` → **load state/fixtures** (E-1606).

Run order (executed by the E-1603 runner): **provision → setup → seed → run → teardown.**

## Deliver

1. **`setup` field** in the manifest schema (extends E-1602's struct): an ordered list of
   commands and/or references to script files. Runs after provisioning, before seed. A failing
   setup step aborts the run loudly, naming the step.
2. **Project-level config `.endless/verify.toml`**: holds shared `setup`, default
   `needs`/`format`, shared fixtures. Merged with each per-task manifest under
   `.endless/tasks/`. Merge semantics: project-level setup runs first, then per-task setup
   appends (per-task may override defaults). Define + document the merge precisely; reuse
   Endless's layered-config approach (go-cfgstore / `.endless/config.json`).
3. **Standard script locations**: `.endless/verify/` for project-shared scripts (e.g.
   `.endless/verify/setup.sh`); per-task scripts beside the task manifest. Short steps may inline
   in TOML; non-trivial setup lives in real script files (liner/editor-friendly).

## Generic vs Endless (no PRODUCT/self_dev leak)

The `setup` field is generic; the *commands* are project-specific. Endless's own setup (build the
worktree binaries + initialize schema into the fresh temp env) lives in Endless's project-level
`.endless/verify.toml`, NOT in product code.

## Verify

- A project-level `.endless/verify.toml` + a per-task manifest merge into the expected effective
  config (project setup then task setup).
- Setup steps run in order before the runner; a failing step aborts loudly with the step named.
- Endless's own `.endless/verify.toml` prepares a fresh temp env so a dogfood suite runs green.

## Deps

- Follows E-1602 (landed manifest schema/discovery). Blocks E-1603 (the runner executes `setup`).
