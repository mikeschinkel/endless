# go-cambria — a forward-only Cambria-lens JSON transform library (Go)

Own go-pkgs module at `~/Projects/go-pkgs/go-cambria`; reusable beyond Endless.
First consumer: the Endless upcasting layer (E-1672). Related to E-1671.

## Objective
A Go library implementing Cambria-style schema **lenses as data**, applied to JSON
documents to translate between schema versions. **Forward-only** (see Decision).
No Go implementation of Cambria exists today (TypeScript original + a Rust port).

## Decision: forward-only (documented + open for input)
Cambria's lenses are bidirectional (one definition runs both directions). This
library ships **forward-only** — translating a document from an older schema toward a
newer one; inverses are not implemented. Rationale: the first consumer (Endless)
replays an immutable log into one current read-model and never needs old readers to
consume new data. **The README must document this decision prominently and explicitly
solicit input from anyone who needs bidirectional lenses** (old-reader-consumes-new-
data, round-tripping), so real demand is visible before we invest in inverses.

## In scope (v1)
- The lens op set as data (struct/JSON), forward semantics: `rename, add, remove,
  hoist, plunge, wrap, head, in, map, convert`. (`convert` carries a forward value
  map only; it is the one non-strict op.)
- A `Lens` = ordered list of ops; apply a lens forward to a JSON document.
- Sequential composition of lenses (apply a chain in order).
- Parse/serialize lenses to/from the data form (the Cambria YAML/JSON shape, adapted).
- Tests with worked examples (the canonical rename+convert; each op; composition).
- README: the model, the op set, and the forward-only decision + the call for
  bidirectional input.

## Deferred (note in README/TODO, not v1)
- Inverses / bidirectional application — gated on the solicited demand above.
- The schema *graph* + shortest-path between arbitrary versions (consumers that need
  only a linear chain compose sequentially).
- Generating JSON-Schema / Go type definitions from lenses (Cambria does this).

## Not this library's concern (it lives in the Endless layer, E-1672)
Migrations / manifest-of-references, content-hash identity, the `applied` set on
events, the per-kind chain walk over a ledger, the `custom` N→M event-split escape
hatch, integrity gates against ledger events. go-cambria knows only JSON documents
and lenses — nothing about events, ledgers, or Endless.

## Conventions
Follows the go-pkgs layout and Mike's Go house rules (ClearPath, doterr, go-dt) like
the sibling libraries; `replace`d into Endless via go.work / go.mod.

## Open logistic (raise at spawn)
Implementation is in the go-pkgs repo, outside Endless's per-task-worktree model, so
how it is spawned/developed may differ from an Endless worktree.
