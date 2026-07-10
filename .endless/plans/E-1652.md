# Brainstorm: migrations + a rebuild-db event-upcasting pipeline

**Type:** brainstorm. **State:** prior-art research is in (see below); reconciling
the report against our discussion to settle a small set of remaining decisions
before this graduates into an implementation epic. No code yet.

## Inputs
- **E-1666** (research, completed) — prior-art survey; its plan is the research
  prompt, its outcome is the full report. Report file:
  `docs/private/research-2026-06-26-event-sourcing.md` (local-only).
- Cambria essay: https://www.inkandswitch.com/cambria/

## Problem (corrected from the original framing)

The original premise — "the E-1252 status rename forced rewriting the committed
ledger" — is **not** what happened. E-1252 did a pure *code* slug-swap
(`needs_plan`→`unplanned`, `in_progress`→`underway`, `verify`→`unverified`) and
**deliberately left the old slugs in the ledger**. The real cost is a **latent
silent-revert landmine**: when E-1532 lands (UPDATE live rows to new slugs) and
anyone runs `rebuild-db`, the projector replays the *old* slugs from the immutable
ledger and silently reverts the live DB; `validate-db` then reports drift.

The codebase **already** does upcasting, ad hoc: `projector.go` / `decision.go`
legacy shims (`mapLegacyDecisionStatus`, `replayLegacyDecisionCreated`, the
`type=="decision"` routing, `projectorTypeID` NULL-ing retired slugs, the dropped
`prompt` skip). This task generalizes that into one registered pipeline; the
existing shims become seed entries (one-time consolidation), append-only after.

## What the research confirmed (locked, validated by prior art)

1. **Identity = stable name, never a sequential integer.** Per-event-type
   `(event-type, transform-name)`, plus a **content hash** stored alongside for
   integrity/dedup (NOT as the primary key). This is the Flyway-named-migration +
   Avro-fingerprint hybrid. Sequential integers are the one scheme proven to
   collide on merge (the pre-2.1 Rails renumber problem). Confirms our instinct.
2. **Declarative transforms as data, Cambria-style, + an imperative escape hatch.**
   Tolerant-reader/weak-schema handles ONLY additive change and silently corrupts
   on rename/value-rename/remove/restructure/split.
3. **Forward-only** (no inverses). We replay into one current read-model; never
   need old code reading new events. Simpler, and admits lossy/one-way/N→M ops.
4. **rebuild-as-migration** (our Option C) is the established pattern
   (Marten/Axon/EventStoreDB). "Migrate the DB" = rebuild the projection.
5. **Content-hash gate at replay:** each event records name+hash of every applied
   transform; replay verifies the hash matches the current definition before
   applying — edited-after-the-fact transforms **fail loudly** (Flyway's
   changed-migration check). Hard gate, not a warning.
6. **Compaction/baselining deferred** (matches our "upcast every replay for now").
   Mechanism documented for when rebuild time hurts.
7. **Scope = multi-dev (locked, 2026-06-27).** Endless's committed ledger WILL be a
   shared repo with multiple committers; multi-dev is a real product goal, not a
   maybe. So the merge-safe machinery (named+content-hash identity, cross-branch
   conflict semantics, atomic cross-kind grouping) is **in scope and required** —
   not YAGNI. Build for concurrent multi-dev authorship. (The upcasting itself is
   needed even single-user; this just settles that the merge layer is also built.)

## Cambria op set (replaces our ad-hoc primitive names)

`rename`, `add`, `remove`, `hoist`, `plunge`, `wrap`, `head`, `in`, `map`,
`convert` (value/enum remap — the declarative escape valve), plus a named
**`custom`** imperative escape hatch resolved through a host-populated registry.

Correction to an earlier idea: a `rewrite_kind` / "route an old event to a
different entity" (the E-1378 decisions shim) is **not** a flat Cambria op — it's
an N→M / split, handled by the **`custom`** escape hatch (the report calls
split-into-new-entity out as requiring a custom N→M transform).

**Promote-a-pattern-to-a-primitive** (rule of three, mechanically tracked) stands —
Cambria's authors describe the same discipline for growing their op set.

## Our additions the report does not cover (still ours, still valid)

- **Ephemeral split + two ledgers.** Durable kinds (task/decision/epic) →
  `.endless/db-ledger/`; ephemeral kinds (session/focus/session_status/…) →
  **gitignored** `.endless/db-ledger/local/`, enforced per-kind at emit time so an
  ephemeral kind cannot reach the committed ledger. Resolves today's "muddy"
  bucket (session_status/focus committed-but-not-replayed).
- **Diagnosability over agent-memory.** Coverage linter (extend `validate-db`):
  when a transform renames a value in field X, flag every event kind carrying X
  that lacks a corresponding transform — catches "forgot a coupled kind." Cheap
  prompt-hook tripwire surfaces ledger/registry drift immediately; full
  projection-diff runs at land.
- **Rebuild trigger:** only on a land-or-pull that adds a transform; endless
  detects the new transform file in the merge and rebuilds in the existing
  land/rebase step (no git hook). Normal work-lands don't rebuild.

## Decisions status — ALL RESOLVED by the round-2 report (E-1666 outcome)

- **A. Transforms as data files — accepted.** Boundary (report §2): table-shape
  changes stay in `internal/schema/changes/*.{sql,go}` + `schema.sql`; event
  value/shape transforms live in `.endless/db-ledger/transforms/<kind>/`. Rebuild
  builds the structural destination FIRST, then replays transforms into it. A
  "move field to a new table" is a **pair** (structural change + transform) landed
  together; the linter checks the pairing.
- **B. Cross-kind coupling — resolved: atomic grouping via MANIFEST-OF-REFERENCES**
  (ADR-002). A migration = a small manifest (`name`, `description`, `covers:
  [kinds]`, entry refs + hashes) + one per-kind entry file each. Keeps merges clean
  (conflict unit stays at the per-kind-entry grain) AND makes coupling structural.
  Grouping + coverage linter are complementary — grouping makes the linter
  *decidable* (`covers` is a declared, hash-checkable set). Atomicity is an
  author/merge-time gain, not replay (rebuild-from-log already can't half-apply).
- **C. Custom-transform hashing — resolved (ADR-001 revised):** declaration-hash
  (replay-time gate) + **mandatory golden-fixture CI tests** (the option I
  proposed, adopted). Body-hashing rejected as decisively over- AND under-sensitive.
- **D. Adopt revised Part II as the implementation brief — yes.** It already folds
  in the scope split, diagnosability, dir naming, and the structural boundary.
- **Scope — resolved (ADR-003), sharper than "multi-dev locked":** *two concurrency
  regimes, both built now.* Regime A (single-dev, no conflicts) is the degenerate
  empty-conflict case of Regime B (multiple worktrees/devs recording conflicting
  entries). Build the FULL reconciliation path now (detect → surface → human
  resolves like a git conflict → correct replay); defer ONLY *automatic* conflict
  resolution, on evidence grounds, not scope. self-dev across worktrees is a live
  Regime-B test bed today.

## Key reframes the report introduced (adopt)

- **"Ephemeral" → SCOPE (project vs machine-user).** The axis is *whose state it
  is*, not durability — a machine-user event can be durable locally yet still wrong
  to commit. `project` scope = the root `db-ledger/` (unmarked default); `machine-
  user` = `db-ledger/local/` (gitignored, marked opt-out). Scope is a **required
  declared property** per kind — hard error if undeclared, because the directory
  default (root = project) and the safe default (undeclared = keep OUT of version
  control) point opposite ways. `session_status.*`/`focus.*` → machine-user
  (eliminates today's muddy bucket).
- **Two collision sites:** identity collision (same file path → git conflict at
  merge) and semantic collision (clean merge, incompatible meaning → caught by
  linter/tripwire/rebuild). Both surfaced now; auto-resolution deferred.
- **Linear per-kind chain walk, not graph shortest-path** (YAGNI guard vs Cambria).

## Next step: graduate to an implementation epic

The brainstorm is resolved; the revised Part II is the implementation brief. An epic
should implement: the std-lib-only Go upcasting package (declarative ops + custom
escape hatch, forward-only, manifest-of-references migrations, name+hash identity,
linear chain walk), the project/machine-user two-ledger scope split enforced at
emit, the coverage linter + per-prompt tripwire, the structural-boundary rebuild
ordering, and the land/pull rebuild trigger. Folds in the E-1667-area worktree work
where they touch the same emit/rebuild paths.

## Deferred / out of scope
- Snapshot/compaction to retire old transforms (only when rebuild time hurts).
- Bidirectional lenses (only if old readers ever must consume new events).
