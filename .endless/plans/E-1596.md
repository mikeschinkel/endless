# Verification-suite system — plan

The design reasoning is in this task's analysis field (`endless task show E-1596 --analysis`).
This is the forward plan.

## Approach: research-first, then incremental

1. **Research** (child task) — a Claude Web deep-research effort answers the open architecture
   questions: the result contract, test-runner orchestration, the portable executable form,
   isolation ranked by user-imposition, seeding primitives, and automating the
   process-replacing / interactive cases. Its findings land in that task's `outcome`.
2. **Deliberate** — review the findings together; lock the result contract, the default
   executable form, and the minimal discovery convention.
3. **Increment** — build in stages, starting with a **"good-enough to start now" tier that
   requires zero external tools**: a convention (where a suite lives, how it's named, a small
   manifest) + the result contract + Endless discovering/invoking the project's test runner and
   normalizing results, with the simplest isolation (self-isolating suite, or the existing
   per-worktree sandbox). This lets real verification suites be written before the full system
   exists.
4. **Defer the rest** — file the granular later/maybe support tasks (mise, containers, Apple
   container, Dev Containers, seeding primitives, PTY/interactive automation, Windows shells)
   only after the architecture settles, so we don't churn tasks the research may reframe. The
   dispositions are recorded in the analysis field as the do-not-block surface.

## Propagation (once the convention exists)

Wire the convention into the spawn handoff templates (the verify-handoff step) and `endless
guide`, so future sessions produce a verification suite for every verify-handoff.

## Out of scope / non-goals

- Not a CI/CD platform; hermetic builds and production parity stay user-owned.
- No external tool may be required to run the core feature.
- Retrofitting the existing ad-hoc tests/tasks/*.sh scripts onto the shared harness is out
  of scope for THIS build, but is now a tracked follow-up (E-1623, child of this epic, blocked
  on the runner + result writer) — not the permanent "never migrate" it implied when only the
  one e-1577 prototype existed.

## Companion deliverable (done, outside this repo)

`~/Projects/beyond-vibe-coding/qa-and-testing.md` — the testing quality strategy written up as
engineering principles for the book (Ch 16). Independent of the Endless implementation.
