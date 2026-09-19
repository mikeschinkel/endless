# E-1672 — Endless event-upcasting layer (on go-cambria)

Child of E-1671. **Depends on go-cambria (E-1678)** for the declarative lens ops.
Brief: E-1666 Part II §5, §6, §8, §12; ADR-001, ADR-002. Design locked; option-free.

## Objective
The Endless-specific event-sourcing layer that upcasts project-scope ledger events to
the current shape at replay, **built on the go-cambria lens engine**. In memory; the
engine the rebuild path (E-1674) calls. No SQLite/CLI; ledger I/O limited to loading
its own migration/transform data files.

## In scope
- **Migration = manifest-of-references** (ADR-002): manifest (`name`, `description`,
  `covers[]`, entry refs + hashes) + one per-kind entry file. Each entry's ops ARE a
  **go-cambria lens**. Loaded from `.endless/db-ledger/migrations/<id>.json` +
  `transforms/<kind>/<id>.json`.
- **Identity = migration name + content hash** over the canonical form (sha256; short
  display prefix). Events carry `applied` = the set of `name@hash`.
- **Linear per-kind chain walk:** apply the kind's migrations not already in
  `applied`, in dependency order (`after` → topological sort; canonical name
  tiebreak). NOT graph shortest-path.
- **`custom` N→M escape hatch:** host-populated registry
  `map[string]func(event)([]event,error)` — event-split is beyond document lenses, so
  it lives here, not in go-cambria. Unregistered custom op = hard error.
- **Integrity gates (all hard errors):** hash mismatch vs the recorded hash;
  unregistered custom op; missing referenced entry file.
- **Golden-fixture CI harness** for custom transforms (ADR-001).

## Out of scope
The lens op set + forward document transform (go-cambria, E-1678); emit-time scope
routing (E-1673); rebuild/projector wiring (E-1674); coverage linter + tripwire
(E-1675); rebuild trigger (E-1676); baseline/compaction (deferred).

## Acceptance criteria
- A rename-value migration (a go-cambria lens) applied to an old event not in
  `applied` yields the renamed payload and adds the migration to `applied`.
- A `custom` op producing 2 events from 1 is handled (slice return).
- Hash-mismatch / unregistered-custom / missing-entry each hard-error naming the
  offender.
- Two independent migrations on different kinds compose; order-dependent ones require
  a declared `after`.
- Tests pass including golden fixtures; depends on go-cambria with no other
  third-party / SQLite / CLI / ledger-writer imports.
