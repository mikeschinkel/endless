# Brainstorm input — first-class-runner launcher resolution within the needs-driven escalation

Exploration material for the brainstorm, NOT a plan or a recommendation. It maps
the open space, grounded in already-locked decisions so settled facts are not
re-litigated. No option here is ranked or endorsed. No "what this should produce"
section — that is the brainstorm's job, not this input's.

## Locked context — do NOT re-open (source of truth)

`docs/private/research-2026-06-19-verification-suite-archictures.md` (and the E-1596
epic analysis) already decided the surrounding model. Treat these as fixed:

- **The isolation model is an escalation ladder, ranked by user-imposition and
  driven by the suite's declared `needs` — not a single tool or a one-size choice:**
  Tier 0 self-isolation (temp XDG + temp SQLite + testscript `$WORK`) → Tier 1
  ephemeral per-run sandboxes → Tier 2 **mise** tool-version resolution (opt-in) →
  Tier 3 containers over the docker-compatible socket → Tier 4 Apple container
  machine / Firecracker micro-VMs. The agent requesting verification declares
  `needs`; Endless escalates only as far as those needs require.
- **Version-manager question is CLOSED:** shell out, do not embed; **mise over
  asdf** (mise's PATH manipulation eliminates asdf's `reshim`); **direnv is
  obviated** by mise. Nix-as-dependency, devbox/flox, proto, Distrobox/Toolbx,
  asdf, direnv are all rejected. These are not brainstorm fodder.
- Runner delegates, never replaces; `verify.toml` is a pointer manifest; CTRF is the
  normalization contract; testscript/txtar is the Go CLI/e2e style. All settled.

The brainstorm operates INSIDE this ladder, not alongside it.

## The genuinely-open gap this brainstorm addresses

The locked ladder resolves tool **versions** (Tier 2 = mise, opt-in) and
**services/OS isolation** (Tier 3/4). It does not address a finer question that
surfaced building the pytest form (E-1605):

At **Tier 0** (self-isolation, no mise, PATH preserved), given a Python already
present, **which launcher actually runs the tests** — `uv run pytest`, a
project-local `.venv/bin/pytest`, a bare `pytest` on PATH — is undetermined. Endless
is uv-managed, so bare `pytest` is not on PATH and the first-class `pytest` check
fails. This is intra-interpreter launcher resolution, one rung finer than tier-2
version resolution, and it must be expressed the same way the rest of the model is:
**as something the requesting agent's declared needs drive, escalating only as far
as required — not as Endless silently guessing one tool over others.**

Mike's directive that seeds the code-organization side: implementation-specific
launcher knowledge for a first-class runner should arrive via a
driver/connector/adapter, not be layered into the generic (app-agnostic) runner.

## Open dimensions (each an independent knob; none pre-decided)

Framed to respect the locked ladder — i.e. "how does escalation express this," not
"which single tool wins."

1. **Who declares the launcher need, and how it drives escalation.**
   - The requesting agent declares it in `needs`/the manifest (e.g. the check states
     the interpreter/launcher requirement), consistent with `needs`-drives-tier.
   - Endless detects the environment (`.venv`, `uv.lock`, PATH) and satisfies it.
   - A project-level verify config states the project's Python tool once.
   (Mirrors the locked ladder's "declare vs. infer" split — mise is opt-in *declared*,
   Tier 0 is *inferred*.)

2. **Where launcher resolution sits relative to the tiers.**
   - A facet of Tier 0/1 (resolve among whatever is already present).
   - Its own sub-rung between Tier 0 and Tier 2 (a launcher tier below the
     version-manager tier).
   - Folded into the existing tiers with no new rung.

3. **What "escalation" means at this granularity** — the ordered set of approaches
   the requesting agent's needs select among (e.g. an ordered attempt sequence vs. a
   single declared launcher vs. tier-by-tier fallback), and who decides the order.

4. **Bare-clone behavior** (`RenderRunScript`, Endless absent): the emitted script
   must still run. Resolve at emit time, emit a self-detecting shell prelude, or
   require the launcher be declared so no resolution is needed.

5. **Code-organization seam (the driver/adapter/connector directive).** How a
   first-class runner contributes its own launcher resolution across the ladder
   without that logic living in the generic runner. Points in this space:
   `firstClassRunner` struct-of-funcs + a launcher hook; a `RunnerDriver` interface
   with a per-ecosystem launcher adapter; a launcher/tool resolver kept as a
   separate axis from selection/format. (Each must accommodate MULTIPLE launchers
   selected by need — i.e. serve the escalation, not encode one tool.)

6. **Generalization to other interpreted ecosystems** (node `npx`/`pnpm`, ruby
   `bundle exec`, …) — whether the seam anticipates them, WITHOUT re-opening the
   version-manager decision (mise remains the Tier-2 answer for versions).

7. **Context threading** — what an in-Go resolver needs (project root, "is uv on
   PATH", tool presence) and how it reaches the emission point (`runChecks` has the
   root today; `RenderRunScript` does not).

## Tensions to surface (not to resolve here)

- Declare vs. infer at this rung — the locked ladder does both (opt-in mise vs.
  inferred Tier 0); which fits intra-interpreter launcher choice?
- Keeping the generic runner app-agnostic vs. giving first-class runners real
  launcher knowledge via the seam.
- Whether launcher resolution is genuinely a new concern or just the low end of the
  existing `needs`-driven escalation wearing a different name.
- Bare-clone self-sufficiency vs. Endless-enhanced convenience.
- One interpreted ecosystem now vs. a seam shaped for several.
