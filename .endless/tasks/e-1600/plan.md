# Deep-research prompt — Endless per-task verification-suite system

This task's deliverable is the FINDINGS of the research below (paste them into this task's
`outcome` when complete). The prompt is written to be run on Claude Web (deep research).

---

**Goal.** Design a **per-task verification-suite system** for Endless (a CLI project-awareness /
task tracker). Each task that reaches a `verify` state should carry a committed, executable
**verification suite** — the complete test set (unit + end-to-end) proving that task's
acceptance criteria. It must be runnable three ways: (a) by a bare `git clone` with **Endless
NOT installed**; (b) by Endless (`endless verify <id>`) on the user's behalf; (c) by an AI agent
(Claude/Codex) so the user needn't run it.

**Hard constraints.**
1. The CORE feature must require **no external tool** — adopting a version manager or container
   engine must never be a precondition to use it.
2. Endless is the **discoverer/orchestrator, never the test runner** — it invokes the project's
   existing runner (`go test`, `pytest`, `bats`, `testscript`, …) and normalizes results.
3. **All-Go future** — no Python/uv lock-in (Python-as-orchestrator may be considered on its
   own merits).
4. CLI-driven and "invisible" is a virtue; GUI-required tooling is a poor fit.
5. Endless is **not a CI/CD platform** — hermetic builds and production parity are user-owned.
6. The design must be **pluggable** so heavier isolation can be added later without redesign.
7. The chosen form must be **trivially authorable by an AI agent** (Claude/Codex) — accurately
   producing a task's verification suite with minimal-to-zero user input.

**Context — how Endless isolates today (build on this, don't reinvent incompatibly).** Endless
self-dev routes each git worktree's DB writes to a per-worktree **sandbox** at
`~/.cache/endless/sandboxes/<worktree-basename>/`, selected by exporting
`XDG_CONFIG_HOME=<sandbox>` (with `bin-sandbox/` wrapper scripts that set it and exec the
worktree-built binary). One sandbox per worktree today; concurrent verify runs that mutate the
task DB would need multiple sandboxes per worktree (a current gap). A verification suite that
self-isolates can do the same trick (temp `XDG_CONFIG_HOME` / temp dirs) with no new tooling.
Treat this as the zero-dependency baseline; escalate to heavier isolation only when a project
genuinely needs it.

**Research questions.**

1. **Result contract.** Is TAP (Test Anything Protocol) still the right language-agnostic
   pass/fail/detail contract in 2026, or do modern alternatives fit better — `go test -json`,
   NDJSON / line-delimited JSON, pytest-json-report, OpenTelemetry test signals, others?
   **Exclude XML-family formats (no JUnit XML)** — prefer lightweight JSON/line-oriented
   contracts. Which best carries non-text outcomes (web-UI behavior, screenshots, API/HTTP
   assertions)? Recommend one and justify.
2. **Test-runner orchestration.** Survey prior art for discovering, invoking, and normalizing
   results across heterogeneous runners and languages: gotestsum, cargo-nextest, tox/nox,
   hatch, dagger, earthly, `act`, moon/turbo test pipelines, bazel test, just/mage/task. What
   is the minimal convention (file location, naming, a small manifest) that lets a third party
   discover and run a project's verification suite without bespoke per-project code? Start
   native support with `go test` and `pytest` (dogfoodable); design for others.
3. **Portable executable form.** Compare as the default and as options: bash + a shared
   assertion/reporting lib; BATS; testscript/txtar (note its all-Go embeddability — Endless
   could vendor the runner); native-framework-per-project. Which default, which optional? How
   should "indirect via bash" wrap native runners? Weight the choice heavily toward what an AI
   agent can author correctly with zero user input (constraint 7).
4. **Isolation, lightest-touch first.** A precursor survey of PATH-managers vs containers
   concluded "mise on host + Compose for services" and "Hermit = self-bootstrapping committed
   `bin/`, zero-prereq clone-and-run." Given constraint 1, rank options by how little they
   impose on the user: self-isolation (temp dirs / temp `XDG_CONFIG_HOME`), reusing an existing
   sandbox, Hermit (does its committed-stub bootstrap uniquely serve the no-Endless clean-clone
   case?), mise (only-if-needed), containers via the docker-compatible socket (engine-agnostic:
   Docker, Podman, Colima, Lima), and micro-VMs. Reject Nix-as-dependency, devbox/flox, proto,
   Distrobox/Toolbx.
5. **Apple's container stack — narrow open question.** Established: a container machine is a
   persistent, host-integrated Linux VM (bring-your-own OCI distro with `/sbin/init`),
   CLI-driven — Apple's Colima/Lima/WSL equivalent; multi-service orchestration is done by
   running Docker/Podman + compose inside it. Confirm Docker/Podman run inside it (standard
   Linux-VM expectation; Podman possibly smoother). Primary question: should container machine
   be Endless's default macOS isolation substrate, and what are the fallbacks for Intel Macs,
   pre-Tahoe macOS, Linux, and Windows? Seeds: github.com/apple/container,
   github.com/apple/containerization, developer.apple.com/videos/play/wwdc2026/389.
6. **Micro-VM isolation.** Evaluate Firecracker (firecracker-microvm.github.io) as a
   per-verification isolation substrate vs. Apple's per-container VMs vs. plain containers —
   startup cost, footprint, CLI-drivability, cross-platform reach.
7. **Version-manager integration mechanics.** Confirm: shell-out vs embed for asdf/mise (is
   embedding even viable?); is asdf's `reshim` a real operational concern; does mise obviate
   direnv; minimal footprint to add tool-version resolution only when a project opts in.
8. **Determinism & quality across languages.** How to make e2e suites simultaneously fast,
   idempotent, repeatable, and real-usage: controlling clock/randomness/ordering/network;
   tiering (smoke vs full); per-run isolation/teardown; failure observability (expected/got
   detail). Cross-platform (incl. eventual Windows) notes.
9. **Concurrent isolation within one checkout.** Endless self-dev routes DB writes to a
   per-worktree sandbox; a verify run that mutates the task DB needs its own isolated DB.
   Patterns for multiple ephemeral sandboxes per worktree (or per-run temp DBs).
10. **Prior art: acceptance-proof-per-ticket.** Do any task trackers or dev tools tie a
    committed, executable test suite to a ticket's acceptance criteria (ticket↔test
    traceability, executable specifications, story-level acceptance tests)? What worked / failed?
11. **Seeding primitives.** A verification suite often needs to set up DB/state preconditions
    (tasks, sessions, background-agent rows, fixtures). Field evidence shows authors reaching
    into internal subcommands because the sandbox exposes no seeding verb. What should a
    first-class, supported seeding surface look like so suites never reach into internals —
    declarative fixtures, an `endless verify seed ...` verb, factory helpers?
12. **Automating the "un-automatable."** How to automate checks that today get punted to MANUAL
    because they replace the runner (`exec` into `claude attach`) or need a live tmux/PTY?
    Evaluate PTY harnesses (expect/pexpect), pseudo-tty capture, and running the suite inside a
    container / container machine so the exec or tmux session happens in a disposable
    environment without killing the test runner. Goal: drive "deliberately not automated"
    toward zero.

**Deliverable.** A recommended architecture with: (i) the chosen result contract; (ii) the
default portable executable form + the optional ones; (iii) the minimal discovery convention;
(iv) a "good-enough to start NOW with zero external dependencies" minimal tier; and (v) a staged
roadmap of optional/later isolation capabilities ranked by user-imposition.
