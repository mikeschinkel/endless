# Plan — E-1608: reset the canonical sandbox at verify-run start (via project hook)

Reshaped from the original "ephemeral per-run sandboxes / concurrency gap." Per
ED-1534 and design discussion: keep one canonical sandbox per worktree (E-1655);
a self-dev verify run just needs that sandbox in a KNOWN state, not the agent's
dev-time cruft. Concurrent verify runs are a non-concern under one-session-one-task
and are explicitly deferred. The generic runner stays app-agnostic (E-1603) — the
Endless-specific reset lives in a project hook, never in verify tooling.

## Deliver

1. **A project-owned reset hook (Endless self-dev).** Endless supplies a hook
   that resets its canonical worktree sandbox to a known state — empty schema,
   then the E-1606 seed. The reset logic is Endless-specific and lives in the
   hook (project-owned); the runner contains none of it.

2. **Fire the project provision hook at the provision step.** E-1603's provision
   precondition (a no-op at Tier 0) fires the project's provision hook so the
   reset runs before the checks. Mechanism decision (deferred): reuse the existing
   project-level `setup` machinery (E-1611: `.endless/verify.toml` setup running a
   `.endless/verify/` script) vs. a distinct `.endless/hooks/` provision hook.
   Lean: reuse existing machinery — add no new mechanism.

3. **verify.toml snapshot opt-in — stubbed.** Add an opt-in field for "snapshot
   the sandbox before reset" so the agent can preserve pre-verify state. It records
   the choice and no-ops (with a clear "not yet available" note) until the sandbox
   snapshot epic (E-1790) delivers save/restore.

## Deferred mechanism (coordinate with E-1667, do not solve here)

Self-dev checks must all target the *same* reset sandbox: `endless-go` resolves it
by cwd self-detect, the Python CLI by `XDG_CONFIG_HOME` — today those can diverge.
Aligning them so both land on the reset canonical sandbox is E-1667's routing
domain; this plan states the requirement and consumes whatever routing E-1667
settles, rather than inventing its own.

## Boundaries

- One canonical sandbox per worktree (E-1655 intact). NO per-run/ephemeral
  sandboxes; NO new sandbox path namespaces.
- NO concurrency isolation (deferred; revisit only if one-session-one-task stops
  holding).
- Runner stays generic — the Endless reset is a project hook (E-1603 boundary).
- Snapshot save/restore itself is E-1790, not here.

## Depends on / relates

- E-1606 (seed) — the "known state" contents.
- E-1790 (snapshot epic) — the opt-in field is a stub until it lands.
- E-1655 (canonical single sandbox, preserved) and E-1667 (routing, deferred).

## Verify — `tests/tasks/e-1608-verify.sh` (esu header; exit 0/1/2)

- Seed a dummy row into the canonical sandbox, run a verify, then assert the
  sandbox is back to the known state (schema + seed) — the pre-run cruft is gone.
- Assert the reset ran via the project hook (e.g. a marker the hook writes), not
  runner-internal logic.
- The verify.toml snapshot field parses and is accepted (stub: recorded, no-op)
  and does not block the run.
