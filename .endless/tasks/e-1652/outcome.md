# Outcome: recommended design for migrations + a rebuild-db upcasting pipeline

**Recommendation: build a forward-only, declarative (Cambria-style) event-upcasting
pipeline so `rebuild-db` replays historical events into the current schema without
ever rewriting the committed ledger.** Full implementation brief: E-1666 outcome
(revised Part II/III). Reconciled decision record: this task's plan (`--text`).

## Problem (corrected)
The E-1252 status rename did NOT rewrite the ledger — it left old slugs in the
immutable ledger, creating a latent **silent-revert landmine**: once E-1532 updates
live rows, a `rebuild-db` replays the old slugs and reverts them. The codebase
already does ad-hoc upcasting (the E-1378 decision shims); this generalizes it.

## Locked design
- **Forward-only declarative transforms as data**, Cambria op set (`rename`, `add`,
  `remove`, `hoist`, `plunge`, `wrap`, `head`, `in`, `map`, `convert`) + a
  host-registered imperative `custom` escape hatch. Linear per-kind chain walk.
- **Identity = named migration + content hash**, never sequential integers
  (collide on merge). Each event records the *set* of migrations applied to it.
- **Atomic cross-kind grouping via manifest-of-references** (ADR-002): a migration
  is a small manifest (`covers` + entry refs/hashes) + one per-kind entry file —
  structural coupling without coarsening the git conflict unit.
- **rebuild-as-migration**: "migrate the DB" = rebuild the projection. Structural
  destination (`schema.sql` + `internal/schema/changes/`) is built first, then
  transforms replay into it; value/shape transforms live in
  `.endless/db-ledger/transforms/`.
- **Custom-transform integrity (ADR-001 revised):** declaration-hash replay gate +
  mandatory golden-fixture CI tests. Body-hashing rejected.
- **Scope, two regimes both built now (ADR-003):** single-dev is the empty-conflict
  case of multi-dev; build the full conflict reconciliation path (detect → surface →
  human resolves → correct replay); defer only *automatic* resolution.
- **Scope split (project vs machine-user)** replaces "ephemeral": `project` kinds →
  committed `.endless/db-ledger/`; `machine-user` kinds (incl. `session_status`,
  `focus`) → gitignored `.endless/db-ledger/local/`. Scope is a required declared
  property per kind, enforced at emit. Eliminates today's muddy bucket.
- **Diagnosability first-class:** coverage linter (land-time, made decidable by
  grouping's `covers`) + cheap per-prompt tripwire. Rebuild triggers only on a
  land/pull that adds a migration.
- **Deferred:** baseline/compaction to retire old migrations; bidirectional lenses;
  graph shortest-path; automatic conflict resolution.

## Follow-on
Implementation epic created (see Links). First consumer: E-1659 (task-type rename).
Research: E-1666 (round 1 + round 2 prompts as plan; revised report as outcome).
