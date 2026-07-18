# E-1789 Synthesis — the first-class-runner driver/adapter architecture

Brainstorm outcome. This records the resolved design; it produces information, not
testable behavior. Decisions and follow-up tasks spawned from it are listed at the end.

## What the brainstorm actually resolved

The seed framed this as "where does launcher resolution sit on the isolation ladder,
and what struct/interface shape do first-class runners plug into." The interview
reframed it: **the launcher question is a symptom; the real design is a spec/implementation
boundary and a three-level type system underneath it.** Once that boundary exists, the
launcher problem, the code-organization seam, and the bare-clone question all resolve as
consequences rather than independent knobs.

## Organizing model: `verify.toml` is a spec; `tbv` is the reference implementation

- **`verify.toml` is a specification**, not an Endless-internal file format. It is intended
  to be extracted to a public artifact (working name **`trust-but-verify.org`**) so that
  anyone can implement a conformant runner in any language.
- **`tbv` (Trust But Verify) is the Go reference implementation.** Today its code lives inside
  `endless-go`; later it extracts to a standalone `tbv` binary. Others may build implementations
  in Rust, TypeScript, etc. Wide implementability is the goal — it is what gives `verify.toml`
  a chance at broad adoption.
- **The verifier is always a Go binary in our stack** (`endless-go` now, `tbv` later).
  "Run with no Endless present" means **no `endless` executable** — never "no Go executable."
  A conformant third-party implementation in another language is an equally valid host.
- **Drivers execute the run themselves** (in Go: `os/exec`, capture, normalize) rather than
  emitting a command string for an outer shell to run. Delegating execution to a shell is a
  second engine Endless does not control and is strictly less robust. "How we run Go tests and
  Python tests" is **implementation code, not part of the documented spec.**

## The three-level type system (the load-bearing design)

Modeled directly on how the durable web specs (IANA media types, HTTP auth schemes) survive
where looser specs (OAuth) fragmented — OAuth left too much to implementations as SHOULD/MAY
and they diverged. The fix is to pin behavior in a registry, not leave it per-implementation.

1. **Level 1 — the `verify.toml` grammar.** `schema`, `task`, the `[[check]]` array, field
   shapes, two-level project + per-task config. The container. It does **not** know how to run
   pytest; it only knows `runner = "<string>"`.

2. **Level 2 — the registered runner-type registry.** Named types (`gotest`, `pytest`,
   `pytest/uv`, `shell`, …), each with a **defined behavioral contract**: selection semantics,
   the result format it must emit, and its launcher/environment-resolution behavior.
   Implementations SHOULD conform. Nonconformance is not a grammar violation but forfeits
   interoperability. This is the tier that keeps `verify.toml` universal — the same file run by
   our Go `tbv` and a hypothetical Rust `tbv` behaves identically **because the contract, not
   the implementation, defines the behavior.**

3. **Level 3 — custom types.** Vendor/experimental runners anyone can mint. Non-interoperable
   by design (see naming, below).

The `runner` value in a specific `verify.toml` therefore *does* "know how" — by selecting a
registered type whose contract defines the how. The grammar stays HOW-agnostic; the instance
file does not.

## Launcher resolution: a declared runner-type variant, not inference

This revised an earlier position in the interview. Rather than have the driver *detect* the
launcher (which reintroduces silent guessing and the `uv.lock`-vs-`poetry.lock` ambiguity):

- **The launcher is a declared type/subtype variant** — `pytest/uv`, `pytest/poetry`,
  `pytest/venv` — each a registered level-2 type with its own contract. The agent authoring
  `verify.toml` **declares** the variant. No detection, no ambiguity, no silent guessing. This
  is the "high burden is correct for an AI author" principle applied: the agent resolves the
  choice by naming it.
- **Bare `pytest`** is retained as a "resolve it for me" convenience for the unambiguous case;
  its contract defines the fallback precedence (e.g. prefer the project venv's plain `pytest`
  executable — which survives HOME-isolation with no network — then `uv run pytest`, then bare
  `pytest`). Detection survives only as the bare family's documented behavior, never as the
  primary path.
- The grammar still sees only `runner = "pytest/uv"`; the `/uv` is level-2 registry semantics,
  invisible to level 1.

This directly fixes the original E-1605 bug (uv-managed project, `pytest` not on PATH): the
suite declares `pytest/uv`, and the registered contract for that type defines the invocation.

## Launcher resolution is NOT a tier

Correcting the seed's framing: the isolation ladder (Tier 0 self-isolation → Tier 4 micro-VMs)
is about **isolation level required**, and the rule is **least isolation that does the job** —
having docker installed does not mean everything runs in docker. The isolation level is
**agent-declared per-suite** via `needs`, using the agent's judgment (this is the *judgment*
knob where an easy default would let the AI skate, so high-burden declaration is correct).
Launcher resolution imposes no new isolation and is orthogonal to the ladder — it is a property
of the runner-type contract, not a rung. Knob #2 from the seed ("where launcher resolution sits
relative to the tiers") is therefore dissolved, not answered.

## The code seam

- Replace the `translate`/`capture` struct-of-funcs registry (`internal/verify/check.go`,
  `firstClassRunners`) with a **`RunnerDriver` Go interface.** A driver **executes** its run
  (selects, invokes, captures the stream, normalizes to the CTRF-subset), owns its level-2
  contract behavior including launcher resolution, and is the single execution path.
- **A generic driver** covers runners with no bespoke driver (Zig, Erlang, arbitrary commands,
  the `shell` type): it execs the declared command and **requires a standardized result stream
  back.** The freedom to verify in any language/tool is blessed; the non-negotiable price of
  admission is emitting a normalizable result (TAP/CTRF) — an arbitrary tool that emits only a
  bare exit code is the low-fidelity path we make friction-y, because it is the one that tempts
  an agent to skate.
- **`RenderRunScript`'s pure-`sh` emission (`internal/verify/runscript.go`) is dropped.** It is a
  second, divergent implementation of the run loop that cannot share the driver's
  execute/capture/normalize logic and drifts the moment a driver does anything richer than a
  one-liner. The "standalone, no Endless" story is `tbv` (one Go binary) or any conformant
  third-party implementation — not an emitted shell script. This walks back a slice of E-1596's
  "triple-runnable" framing (the bare-clone-runs-a-shell-script leg); the epic's principle text
  needs a corresponding update.

## Custom-type naming (level 3)

- **No `X-` prefix.** RFC 6648 deprecated it for the reasons that bite here: no namespacing
  (two vendors both mint `x-mytest` and collide) and a broken graduation path (`x-foo`→`foo`
  strands everyone on `x-foo` forever).
- **Steal IANA's grammar exactly — two separators, two jobs.** `/` separates family from
  variant (`pytest/uv`); `.` nests namespace segments in the vendor tree (`vnd.newclarity.foo`).
  Never `vnd/newclarity/foo` (overloads `/`) and never `vnd-newclarity-foo` (hyphens do not nest).
  A custom type within a family: `pytest/vnd.newclarity.uv`.
- **`vnd.` chosen over reverse-DNS** for consistency with the media-type lineage.

## The registry

- **The registry is a `README.md` + a JSON file committed to a GitHub repo.** No infrastructure.
- **Bare names are curated** — a registration PR must include the behavioral contract; a name
  with no real contract is rejected on review. The PR-to-merge *is* the anti-spam gate. This is
  the scarce, interoperable tier.
- **`vnd.*` is free-floating — no registration.** Ownership is by convention (use a namespace
  tied to something you control, same social contract as Java packages / Apple UTIs). Collisions
  barely matter because `vnd.*` is non-interoperable by definition. Requiring `vnd.*` registration
  would re-introduce the exact gatekeeping pressure we pushed out of the bare registry, so it must
  stay free.
- **Consequence, stated as a feature:** a `vnd.newclarity.foo` type is only runnable by an
  implementation that has that vendor's driver loaded. `tbv` cannot run a private type it has
  never heard of, and should not pretend to. Private types stay private until their author
  promotes one to a bare name via the reviewed PR and publishes the contract. **Free to mint,
  not free to interoperate.**
- **Graduation** is by registering a bare alias while the `vnd.*` name stays valid — a deliberate,
  reviewed PR, rare by construction, with no forced rename (the `X-` scar avoided).
- **Registry JSON carries a `status` axis:** `registered` (curated, interoperable) vs. `vendor`
  (an optional courtesy listing of a `vnd.*` contract for discoverability, no interop promise) —
  plus the vast unlisted `vnd.*` ether.

## Parked / future considerations (NOT decisions, NOT scheduled)

- **Paid `vnd.*` registration for sponsors.** A possible model: long-term (prepaid) sponsors of
  the Trust-But-Verify project receive a reserved `vnd.*` registration, sponsor amount scaled by
  org size. Way down the road and explicitly not locked — flagged here so it is not lost. Note the
  can of worms: monetizing vendor registration pressures the exact scarce interoperable tier this
  design protects, and risks the perception problems that dog pay-for-standing registries.
- **The public spec extraction itself** (`trust-but-verify.org`: the grammar spec, the registry
  repo, the standalone `tbv` binary) is the long-horizon open-standard play, tracked as a `later`
  follow-up.

## Spawned from this brainstorm

Decisions (ADRs):
- **ED-1535** — verify.toml is a portable spec; tbv is the Go reference implementation; three-level
  type system governs interop. (decides E-1791, E-1793)
- **ED-1536** — launcher/environment is a declared runner-type variant (pytest/uv), not inference.
  (decides E-1791)
- **ED-1537** — custom runner-type naming: no X-; IANA grammar; curated bare-name registry,
  free-floating vnd.* tree. (decides E-1793)

Tasks (all `cleans_up` E-1789):
- **E-1791** (now) — replace the struct-of-funcs seam with a RunnerDriver Go interface; add the
  pytest/uv driver. Blocks E-1605.
- **E-1792** (next, blocked_by E-1791) — retire RenderRunScript's pure-sh emission; update E-1596's
  triple-runnable framing.
- **E-1793** (later) — extract the verify.toml spec + runner-type registry + standalone tbv
  (trust-but-verify.org). Holds the parked paid-vnd-registration idea.

## Cross-references

- Parent epic: E-1596 (verification-suite system; Interface Contract §A defines the two-level
  `verify.toml` + `[[check]]` array this builds on).
- Directly unblocks: E-1605 (pytest slice) once the `RunnerDriver` seam + `pytest/uv` driver land.
- Current seam code: `internal/verify/check.go`, `internal/verify/runscript.go`,
  `internal/verifycmd/env.go`, `internal/verify/normalize.go`.
