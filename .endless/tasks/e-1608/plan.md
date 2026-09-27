# Plan — E-1608: verify always starts from a fresh sandbox

Re-specced by E-2182. Replaces the earlier "project hook, never in verify tooling" plan.

## Deliver

1. **`endless sandbox reset`** (user-facing; lands on the Go CLI per E-1063). Clears the worktree's canonical sandbox, applies Endless's standard contents, then runs the project's seeding hook. The single front door for resetting a sandbox — callers never invoke the hook directly.
2. **Verify calls it first.** `endless task verify` runs `sandbox reset` before any check, every run. Built into the command, not the suite script.
3. **Snapshot opt-in stays a stub** in verify.toml until E-1790 delivers save/restore.

## Boundaries

- One canonical sandbox per worktree (E-1655). No per-run ephemeral sandboxes, no concurrency isolation.
- Removing the stale XDG freshness claim in internal/verifycmd/verify.go belongs to the XDG phase-out task, but this task must not rely on XDG isolation for freshness.
- Snapshot save/restore itself is E-1790.

## Why the re-spec (E-2182)

The earlier spec kept the reset out of verify tooling to uphold E-1603's app-agnostic boundary. New information: verify silently lost its fresh DB when E-1964 moved the DB out of XDG_CONFIG_HOME, because freshness came for free from XDG isolation rather than being designed in. Freshness is now an explicit verify responsibility, delivered through an endless command (not a direct hook call) so gates added later cannot be bypassed. `endless-go sandbox init --mode worktree` alone is not enough: it seeds only Endless's own rows. The "sandbox is the project's business" wording is a guideline — Endless may add standard contents. Seeding cost is paid every run by design; revisit only if relying on state between runs becomes a real need. Also consumed by E-2184: after a migration renumber the agent resets its sandbox, because goose tracks applied migrations by number.
