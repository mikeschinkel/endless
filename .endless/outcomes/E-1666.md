# Schema Evolution & Upcasting for Event-Sourced Systems: Survey, and a Design for the Git-Merged JSONL Log (Endless)

> **How to read this document.** Part I is the research survey of prior art on event-sourcing schema evolution and upcasting (unchanged from the original report). **Parts II and III are revised:** they integrate four Endless-specific context items the original prompt omitted — the existing structural-migration system, the two-ledger scope split (project vs. machine-user), first-class diagnosability, and the land/pull rebuild trigger — and resolve three design points: atomic cross-kind grouping (ADR-002), custom-transform hashing (ADR-001, revised), and concurrency scope (ADR-003). Part II is the concrete design and implementation brief, self-contained enough to hand to a Claude Code session that will write the Go package; its §0 sets the governing scope (two concurrency regimes, both built now) and the explicit needed-now vs. deferred partition. Part III holds the decision records. Parts II and III restate the load-bearing conclusions from Part I so they can be acted on without re-reading the survey.

---

# Part I — Research Survey: Prior Art

## TL;DR
- **Use a per-event-type *set* of named, content-addressed transforms — not a global monotonic counter** — because sequential integers and global versions collide on git merge exactly the way pre-Rails-2.1 sequential migration numbers did, whereas membership-in-a-set and hash IDs merge cleanly.
- **Adopt declarative, Cambria-style transforms (rename, add, remove, hoist/plunge, wrap/head, convert) as data, with an imperative escape hatch** for transforms too complex to express declaratively; tolerant-reader/weak-schema handles additive changes only, and silently does the wrong thing on renames, value/enum renames, removals, and restructurings.
- **Unify "migration" with "projection rebuild"**: since your SQLite read-model is rebuilt by replay, treat every schema change as a rebuild-from-log (the established "rebuild-as-projection" pattern in Marten/Axon/EventStoreDB), and periodically **compact old segments into a current-schema baseline** so ancient upcasters can be retired.

## Key Findings

1. **Every mature event-sourcing framework treats upcasting as read-time transformation against an immutable log, but they disagree on the versioning unit.** Axon uses a **per-event-type revision string** (`@Revision`) plus a chain of upcasters; Marten uses **per-event-type-name** mapping with optional integer schema version; Kafka/Confluent uses **per-subject schema IDs** with compatibility *modes*; Avro uses a **content fingerprint** of the writer schema. None of these use a single global counter for events.
2. **The versioning-identity question is the crux of your unusual constraint.** Sequential integers (Axon revision `1,2,3`) and global versions collide under concurrent authorship; named-migration *sets* (Flyway/Rails/Django) and content-addressed hashes (Avro fingerprints) survive merge because identity is membership/content, not ordinal position.
3. **Tolerant reader handles additive changes; explicit upcasting is mandatory for the non-additive ones** — field rename, field removal-with-meaning, value/enum rename, nested restructuring, and splitting one event into many. On these, a tolerant reader silently produces wrong state rather than failing.
4. **Mature event-sourced systems already unify "migrate the DB" with "rebuild the projection from the log."** Marten rebuilds projections via its async daemon; Axon "resets" and replays; EventStoreDB re-runs projections. This is "rebuild-as-migration," and it is exactly your model.
5. **Project Cambria (Ink & Switch) is the closest prior art to your exact problem**: declarative bidirectional lenses that translate data across schema versions in a *distributed, no-central-authority* setting, integrated with the Automerge CRDT. Dolt (Git-for-SQL) and Irmin (Git-like mergeable database) show the data-in-Git merge model but punt schema-merge to manual conflict resolution.
6. **Cambria is the canonical declarative transform DSL**: a small set of named lens operations expressed as YAML/JSON data, each with an inverse, composed into a graph traversed by shortest path. Its authors explicitly anticipate adding new lens primitives (e.g., `tail`, `lastAdded`, sort/filter) when recurring needs appear — the "promote a pattern to a primitive" precedent.
7. **Compaction/snapshotting retires old upcasters by folding history into a current-schema baseline** — analogous to Kafka log compaction and aggregate snapshotting — at the cost of rewriting "immutable" history, so it must be a deliberate, versioned, auditable operation.

## Details

### 1. Event schema evolution / upcasting in established frameworks

**Axon Framework.** Axon stores, alongside each serialized event, the fully-qualified type name **and a revision string** produced by a `RevisionResolver` (the `AnnotationRevisionResolver` reads an `@Revision("…")` annotation on the payload class; other resolvers use `serialVersionUID`, a fixed value, or the Maven artifact version). Upcasters implement an interface operating on a stream of `IntermediateEventRepresentation` objects and are composed into an `EventUpcasterChain`: each upcaster transforms an event of revision *x* into revision *x+1*, and the chain stacks these so any old event is walked up to current. Per the docs, "Upcasters are classes that take one input event of revision x and output zero or more new events of revision x + 1… processed in a chain, meaning that the output of one upcaster is sent to the input of the next." This supports 1→many and stateful upcasting. **Versioning unit: a per-event-type revision string.** *Failure modes:* upcaster ordering is not enforced by the framework (a Trifork blog documents using `@Order` annotations and a JUnit reflection test to catch copy-paste errors in INPUT/OUTPUT revision constants — it once caught a forgotten TypeName change); a missing or mis-named revision breaks the chain silently.

**EventStoreDB.** EventStoreDB is a journal, not an upcasting framework — it has **no built-in upcaster mechanism**. The community pattern is either (a) tolerant-reader mapping in the consumer, or (b) **copy-and-transform**: read the old stream, transform events, write them to a new stream, optionally leaving a "link" event. As Michiel Rook and Oskar Dudycz both note, this disturbs global event chronology and requires rebuilding projections from the new stream. **Versioning unit: event type name (often suffixed `_v2`).**

**Marten (.NET).** Added upcasting in v5.9.0/5.10.0. Events are mapped by **event type name** (e.g., `product_item_added_to_shopping_cart`, `…_v2`, `…_v3`); `MapEventTypeWithSchemaVersion<T>(3)` and `.Upcast<T>(2, …)` attach an integer schema version per type. Upcasting is "a process of transforming the old JSON schema into the new one… performed on the fly each time the event is read… a pluggable middleware between the deserialisation and application logic." Two flavors: **CLR-type transformation** (keep old class, write a function old→new) and **raw-JSON transformation** (`JObject`/`JsonDocument`). **Versioning unit: per-event-type name + optional integer schema version.** *Failure modes:* upcasting runs on every read, so external calls inside an upcaster cause N+1 performance problems.

**Eventuous / NEventStore.** These follow the same family: type-name/registry mapping with explicit serializer-level transformation hooks (a TypeScript example in OneUptime's guide shows an `UpcasterRegistry` keyed by event type, sorting upcasters by `fromVersion` and chaining N→N+1). **Versioning unit: per-event-type integer/version.**

**Kafka + Confluent Schema Registry.** Identity is a **schema ID per subject** (usually `<topic>-value`); evolution is governed by *compatibility modes* checked at registration time, not by transforms. The modes: **BACKWARD** (default — new schema can read data written with previous schema; allows add-optional/remove-field), **FORWARD** (old schema can read new data; allows add-field/remove-optional), **FULL** (both), and the **transitive** variants (`BACKWARD_TRANSITIVE`, etc.) that check against *all* prior versions rather than only the latest. Per Confluent's docs, "The main reason that BACKWARD compatibility mode is the default, and preferred for Kafka, is so that you can rewind consumers to the beginning of the topic," and "For Kafka Streams, only BACKWARD compatibility is supported." Newer "schema migration rules" / data contracts allow arbitrary transforms beyond what FULL permits. **Versioning unit: per-subject schema ID, plus a compatibility policy.**

**Apache Avro.** Reader/writer schema resolution: data is always written with the **writer's schema**, read with the **reader's schema**, and differences resolved by rule — if the writer has a field the reader lacks, it's ignored; if the reader has a field the writer lacks, the reader's **default** is used; **if no default exists, it's an error.** Crucially, **you cannot rename a field**; you add **aliases** that map the writer's name to the reader's. Enums need a declared default for symbols dropped from the reader. Avro also defines a **Parsing Canonical Form** and a **fingerprint** (e.g., CRC-64-AVRO) — a hash that uniquely identifies a schema. **Versioning unit: a schema fingerprint/hash.** *Failure modes:* missing defaults turn an evolution into a hard decode error; rename without alias silently drops data.

**Protobuf.** Compatibility rides on **field numbers** (the wire format carries numbers, not names), so **renaming a field is safe** but **changing or reusing a field number corrupts data.** Removed fields must be marked `reserved` (number and name) to prevent reuse. New fields must be optional (proto3 default). `oneof` evolution is hazardous (Robert Yokota documents how moving fields in/out of a `oneof` can silently clear set values, with the retained field even varying between implementations). **Versioning unit: field numbers within a message (no global version).** This is the purest "identity = stable local key" scheme and the most merge-friendly of the serialization formats.

### 2. Versioning identity: integers vs. named-migration-sets vs. content-addressed

This is the decisive section for your constraint.

**(a) Sequential integers per event type (Axon revision 1,2,3…).** Two developers on separate branches both author "the next revision" of `OrderPlaced`. Alice writes revision `2` and an upcaster `1→2`; Bob independently writes revision `2` and a different upcaster `1→2`. On merge, **both claim the same ordinal** — a collision that requires renumbering one of them to `3` *and* rewriting its upcaster's INPUT/OUTPUT constants, *and* any events already written with the loser's `2`. This is structurally identical to the historical Rails problem: per the Rails Guides, "Prior to Rails 2.1 the migration number started at 1 and was incremented each time a migration was generated. With multiple developers it was easy for these to clash requiring you to rollback migrations and renumber them." **Verdict: collides under merge.**

**(b) Set of applied named migrations (Flyway / Rails / Django).** Here identity is **membership in a set**, not an ordinal. Flyway records each applied migration in `flyway_schema_history` by version + description + **checksum** (and recalculates the checksum on every run, raising an error if an applied file changed); Rails records each migration's **timestamp version** in `schema_migrations`. The genius of the model for merging is that **the database's state is "the set of migration IDs that have been applied"** — order of *authoring* doesn't have to be globally agreed. Rails switched to **UTC timestamps** (`20240502101659_add_part_number`) precisely because of the pre-2.1 clash problem; when Bob pulls Alice's migrations, per the Guides, "Rails knows that it has not run Alice's two migrations so it executes the up method for each migration." Flyway Desktop similarly "generates a timestamp as part of the version by default… [to] avoid conflicts if two team members simultaneously add migrations." The residual problem (the "migration collision") is purely *ordering*: if two timestamped migrations touch the same object, or if a lower-versioned migration appears after a higher one already ran, you get Flyway's "Detected resolved migration not applied" — solved by **out-of-order execution** flags or by the fact that *additive, independent* migrations commute. **Verdict: survives merge, because identity is set membership; only genuinely conflicting (same-target) changes need human resolution.**

**(c) Content-addressed / hash-based schema IDs (Avro fingerprints).** Identity is a hash of the schema's canonical form. Two developers who make the *same* change independently produce the *same* fingerprint (automatic de-duplication); two who make *different* changes produce *different* fingerprints that never collide. There is no counter to renumber. **Verdict: survives merge; strongest collision resistance, at the cost of human-unreadable IDs.**

**Conclusion for identity:** sequential integers are the *only* common scheme that breaks, and it breaks for the same reason renumbered migrations break. **Named migrations (a set keyed by stable name) and content hashes both survive git merge without a renumbering ceremony.**

### 3. Weak-schema / tolerant-reader vs. explicit upcasting

Martin Fowler's **Tolerant Reader** (2011) says a consumer should "ignore unknown fields," use defaults for missing fields, and "extract only what you need" — derived from Postel's Law. Fowler specifically warns against "schema-driven binding of your service endpoints… code-generating C# classes from an XSD definition," which is brittle. Greg Young's **"weak schema"** is the event-sourcing application: instead of deserializing to a strict type, you *map* from JSON to an event object — "If a property has the same name in the two we copy the value… If something in the JSON data is missing in the event we just leave the corresponding value" at its default. Young's rule of thumb: "A new version of an event must be convertible from the old version of the event. If not, it is not a new version of the event but rather a new event."

**Where tolerant reader works:** purely additive changes — add a field (defaulted), add an event type, widen a type compatibly.

**Where tolerant reader silently does the wrong thing and explicit upcasting is required:**
- **Field rename** (`completed` → `status`): the reader sees neither the old field (now unmapped) nor a populated new field, and silently uses the default — **data loss disguised as a default**.
- **Value/enum rename** (status `"active"` → `"open"`): the field maps fine, so no error, but every historical value is now an unrecognized string or wrong enum.
- **Field removal with meaning** (a field that *fed a computation*): tolerant reader drops it silently; the projection computes a different number.
- **Restructuring nested shape** (flat `clientId` → nested `client.id`): the old flat field is unknown to the new reader; defaults populate an empty nested object.
- **Splitting one event into a new entity/stream** (`OrderCompleted` → `OrderPaid` + `OrderShipped`, or moving a field to a new table): impossible for a tolerant reader — requires an N→M transform.

The dividing line is **semantic identity of a field/value**: tolerant reader is safe only when the *name and meaning* of every consumed field is stable. (Note the documented downside even in API contexts: the iluwatar pattern catalog warns tolerant reader "may result in silent failures if important data is ignored.")

### 4. Forward-migrating a live read-model vs. replay-time upcasting

There are two philosophies:
- **In-place forward migration** (Flyway/Liquibase/Rails): the database is the mutable source of truth; you run `ALTER`/`UPDATE` scripts forward, each applied exactly once and recorded. There is no log to rebuild from.
- **Rebuild-as-migration** (event sourcing): the **log is the source of truth and the read-model is derived/disposable**, so a schema change to the read-model is just *throw it away and replay*. As Oskar Dudycz puts it: "If we treat events as the source of truth… we can also rebuild our read models," either by truncate-and-replay (with downtime) or **blue/green** (build the new model in a new table/schema, catch up, then switch reads).

Mature systems implement exactly this. **Marten** rebuilds projections through its async daemon (`RebuildProjectionAsync("Shop")`), and supports zero-downtime rebuilds via **projection versioning** — increment `ProjectionVersion`, the new version writes to separate tables, runs Async to catch up alongside the old ("blue/green"), then the old tables are dropped. **Axon** has replay/reset of tracking event processors. **EventStoreDB** re-runs projections from the log.

**When rebuild-from-log is cheaper/safer than in-place migration:** when the transformation is complex or risky (a bad in-place `UPDATE` is hard to reverse, but a replay is idempotent and re-runnable); when you want the new and old models to coexist for validation; when the change is to *derived* data that the log can regenerate exactly. **When it is prohibitively expensive:** when the log is enormous relative to compute (replay is O(all events)); Marten's own history shows the bottleneck — "a global incrementing integer for all cursors: map-type projections can't be run in parallel… Read model database rebuild time went up steadily." This motivates snapshotting/compaction (§7). For your SQLite-rebuilt-from-JSONL system, **you have already chosen rebuild-as-migration**; the cost question is just "how long is a full replay, and when does that force baselining?"

### 5. Local-first / distributed-log systems where the log itself is merged

This is where your problem is genuinely unusual, and where the prior art is thin but pointed.

**Martin Kleppmann's work** frames the foundations: his "Schema Evolution in Avro, Protocol Buffers and Thrift" (2012) is the canonical comparison; his CRDT/Automerge research and the **"Local-First Software"** essay (Kleppmann, Wiggins, van Hardenberg, McGranaghan, 2019) establish the setting where "no single authority controls the data format" and devices sync peer-to-peer.

**Automerge** is a JSON CRDT: concurrent edits on different nodes merge automatically. But Automerge by itself does not solve *schema* change — if one node's software writes a new shape, older nodes need a way to read it.

**Project Cambria** (Geoffrey Litt of MIT CSAIL, with Peter van Hardenberg and Orion Henry of Ink & Switch; *Schema Evolution in Distributed Systems with Edit Lenses*, PaPoC '21, April 26, 2021) is the direct hit. Per its abstract, "We propose a principled replacement: an isolated software layer that uses bidirectional lenses to translate data between schemas on demand to maintain backward and forward compatibility." It is implemented in TypeScript and **integrated with Automerge** for peer-to-peer editing. Its key mechanism for the merged-log case: it stores the **writer's schema with each change** ("self-describing documents"); when a client applies the log, "each change is first evolved into the desired schema before being applied… the writer schema was saved, [so] we can use Cambria to convert the patch from the writer schema to the reader schema before applying it. The result is a log of patches in terms of the reader schema, which can be replayed into a final document in that schema." Schemas form a **graph connected by lenses**, and translation between any two versions takes the **shortest path** through that graph. This is precisely "events written under whatever schema each offline developer had, replayed into the current schema." The essay stresses that the benefits "take on particular importance in decentralized software, where no single authority controls the data format." *Caveats Cambria itself flags:* it is "still immature software, and isn't yet ready for production use"; it cannot *look up* missing data (a change that requires new data that isn't derivable has no good answer); and perfect compatibility is impossible across some changes (a single→multiple assignee change can't faithfully round-trip in the old client).

**Dolt** ("Git for data") is a SQL database with real `branch`/`merge`/`clone`; it does **cell-wise three-way data merge** and detects schema conflicts (`dolt_schema_conflicts`, `dolt_conflicts` tables) but **does not auto-merge incompatible schema changes** — it surfaces them for manual resolution via `dolt_conflicts_resolve()`. Notably, Dolt's own guidance on schema migration across branches mirrors your problem: one option is per-branch migration directories, another is replaying a migration on every branch. **Irmin** (OCaml, MirageOS) is a "Git-like distributed database" with **user-defined merge functions** and mergeable datatypes (CRDTs — LWW registers, queues, sets), and a Git-compatible on-disk format. Both Dolt and Irmin prove the *data-in-Git-merges-like-code* model is real, but **both leave schema-level merge semantics to either manual conflict resolution (Dolt) or custom merge functions (Irmin)** — neither gives you Cambria's declarative cross-version translation for free.

### 6. Declarative transform libraries (transforms as data) + the escape hatch

**Cambria lenses are the prime example of declarative, bidirectional schema transforms.** A lens is specified in YAML/JSON as a list of named operations; **the same single definition runs forward and backward** ("When a developer defines a single lens, it can translate data in both directions, eliminating the need to specify separate forward and backward translations"). The confirmed operation set (YAML keys, with inverse pairings) is:

- **`rename`** (`source`, `destination`) — rename a property; self-inverse. Consistency relation: old name's value == new name's value.
- **`add`** (`name`, `type`, `items`) — add a property with a default (e.g., empty array); inverse of `remove`.
- **`remove`** (`name`) — remove a property; inverse of `add`.
- **`hoist`** (`name`, `host`) — lift a nested property up into the parent; inverse of **`plunge`** (push a property down into a nested object).
- **`wrap`** (`name`) — convert a scalar into a single-element array; inverse of **`head`** (take the first array element as a scalar).
- **`in`** (`name`, `lens:`) — apply a sub-lens inside a named (nested-object) property; **`map`** (internally `LensMap`) — apply a sub-lens to every array element.
- **`convert`** (`name`, `mapping` with forward + reverse value maps, `sourceType`, `destinationType`) — value/enum remapping (e.g., boolean `false`↔`"todo"`). Notably, **`convert` is the one operator that is *not* a strict lens** — because arbitrary forward/backward maps can't guarantee a consistency relation — which is itself the documented "escape hatch within the declarative system."

Example (verbatim from the essay) showing a rename + value-convert composed:
```yaml
- rename: { source: complete, destination: status }
- convert:
    name: status
    mapping:
      - { 'false': todo, 'true': done }
      - { todo: false, inProgress: false, done: true, default: false }
    sourceType: boolean
    destinationType: string
```

**Composition:** lenses accumulate into a graph (nodes = schemas, edges = lenses); arbitrary version-to-version migration is the shortest path. Cambria additionally, per the paper, "creates two additional artifacts based on the same lens definitions used for runtime data transformations: 1) static type definitions in Typescript, and 2) runtime-checkable schema definitions in JSON Schema" — so the declarative spec is the single source for runtime transform, compile-time types, and runtime validation.

**The "promote a repeated pattern to a primitive" precedent:** Cambria's authors explicitly anticipate growing the operator set as recurring needs appear: "when converting between arrays and scalar values, we currently provide a `head` lens, but perhaps a `tail` or a `lastAdded` lens would be helpful," plus "lenses that deal with sorting, filtering, and merging or splitting arrays." They also note that some useful transforms "only work successfully on a subset of data, or only in one direction… These would not, strictly, be lenses" — i.e., the escape hatch for transforms too complex to express as a clean bidirectional primitive. The general thesis is to replace ad-hoc imperative "shotgun parsing" migration code with reusable declarative operators. Other declarative-migration DSLs in adjacent spaces include Avro's alias/default mechanism (declarative reader/writer resolution) and Confluent's data-contract migration rules.

### 7. Compaction / snapshotting to retire old upcasters

The problem: upcaster chains grow without bound; you eventually have a `NullTo1`, `1To2`, … `8To9` chain run on every read of ancient events. Two established mechanisms bound this:

**Kafka log compaction.** A retention policy (`cleanup.policy=compact`) that keeps only the **latest value per key**, discarding superseded records; deletion is via **tombstones** (null-value records). Compaction runs in the background by recopying segments when the **dirty ratio** (`min.cleanable.dirty.ratio`, default 0.5) is exceeded and segments roll (`segment.ms`/`segment.bytes`); tombstones themselves are retained for `delete.retention.ms` (default 86,400,000 ms = 1 day) so consumers replaying from offset 0 still observe deletions before they vanish — the docs note this "gives a bound on the time in which a consumer must complete a read if they begin from offset 0 to ensure that they get a valid snapshot." The lesson: **compaction is asynchronous, threshold-triggered, and carefully preserves a window so replayers aren't corrupted.**

**Aggregate snapshotting.** Rather than replay a stream from zero, persist the aggregate's state at version N and replay only events after N. This bounds *replay length*, not upcaster count, but the idea generalizes.

**Baseline/compaction of the event log itself** (the mechanism you want): periodically read the whole log, upcast every old event to the *current* schema, and write a **new baseline segment that is already in the current shape** (Greg Young's community calls these "Initialized" / migration events that "truncate" history at a point — e.g., an end-of-financial-year baseline, modeled on how accountants close books). Once a baseline exists and all events before it are folded in, **every upcaster whose input revision no longer appears anywhere at or after the baseline can be deleted.**

**When is an upcaster safe to delete?** When no event in the live log (post-baseline) is still at or below that upcaster's input revision — i.e., the baseline has "absorbed" all events that needed it. This requires either (a) a guarantee that the baseline covers everything older, or (b) a scan proving no remaining event needs the transform.

**The immutability tension:** baselining **rewrites history**, directly contradicting the append-only guarantee. Marten/EventStoreDB copy-transform and Kafka compaction all make the same trade: you give up bit-for-bit immutability of ancient records in exchange for bounded replay/transform cost. The discipline is to make baselining a **deliberate, recorded, reversible-by-re-derivation** operation, never an in-place silent edit.

## Recommendations (from the survey)

For Endless — append-only **JSONL segments in git**, SQLite read-model rebuilt by replay, multiple offline developers merging/rebasing — the survey points to:

1. **Versioning identity: a per-event-type *set of named, content-addressed transforms* — never a global counter.** Tag each event with the *set of named transforms* its writer had already applied; give each transform a **stable human-readable name + a content hash**. This is the Avro-fingerprint + Flyway-named-migration hybrid: identity is *membership in a set keyed by stable name/hash*, which survives git merge with no renumbering. Do **not** use Axon-style sequential revision integers — the one scheme proven to collide.
2. **Transforms: declarative-as-data (Cambria-style) with an explicit imperative escape hatch.** Express common cases as declarative transform records stored in git alongside the log; provide a named imperative escape hatch for N→M splits, lookups, and one-directional conversions. Promote recurring escape-hatch patterns to first-class declarative ops.
3. **Unify migration with projection-rebuild** — treat every schema change as a rebuild-from-log; use blue/green rebuild for zero-downtime.
4. **Compaction/baseline-ing to retire old upcasters** — periodically emit a current-schema baseline segment, retire transforms no live event needs, and keep baselining a deliberate, git-committed, auditable operation.
5. **Forward-only is sufficient** for replay-into-one-model; reserve bidirectional lenses only if old readers ever need to consume new events.

Part II turns these into a concrete, locked design.

## Caveats (on the survey)
- **Cambria is explicitly pre-production** ("still immature software, and isn't yet ready for production use," per its README); treat it as a design template and proven research result, not a dependency to adopt wholesale. Re-implementing its small lens set for forward-only JSONL transforms is the pragmatic path.
- The **exact internal TypeScript identifiers** for Cambria's lens operations could not be verified against source in this research (GitHub raw/source fetches were blocked); the YAML operation keys (`rename`, `add`, `remove`, `hoist`, `plunge`, `wrap`, `head`, `in`, `map`, `convert`) are confirmed from the essay's live examples and a faithful Rust port, and the "promote a primitive" framing is paraphrased from Cambria's "Additional lenses" future-work section rather than a verbatim aphorism.
- **Dolt and Irmin** demonstrate data-in-git merge but **do not solve declarative cross-version schema translation** — they surface schema conflicts for manual resolution (Dolt) or require custom merge functions (Irmin). They validate the merge model, not the upcasting model.
- Framework specifics evolve: Marten's upcasting API and rebuild/zero-downtime features are from v5.9+ and ongoing "Critter Stack" work; Axon's upcaster classes differ across 3.x/4.x. Confluent's "schema migration rules"/data contracts are relatively new and extend beyond the classic compatibility modes.
- The recommendation favors **forward-only** transforms for the replay-into-one-model case; this is a deliberate simplification of Cambria's bidirectional model and would need revisiting if forward compatibility (old readers, new data) becomes a requirement.

---
# Part II — Design & Implementation Brief for the Endless Go Package (Revised)

**Audience:** the implementing session (Claude Code) that will write the Go package. This part is self-contained; it restates the conclusions from Part I so they can be acted on without re-reading the survey. **This revision integrates four Endless-specific context items (existing structural-migration system, two-ledger scope split, first-class diagnosability, and the land/pull rebuild trigger) and resolves three design points (atomic cross-kind grouping, custom-transform hashing, and concurrency scope).**

**Scope:** design decisions, data model, type sketches, file layout, semantics, and failure modes. It stops short of finished code so the implementing session can make idiomatic choices, but every load-bearing decision below is **locked** unless marked otherwise.

---

## 0. Scope: two concurrency regimes, both built now

The single most important framing for this revision. Endless must be correct under **two concurrency regimes simultaneously**, and both are built and tested now — this is not "design for a future second user."

- **Regime A — single developer, no conflicting entries.** One human, possibly across several git worktrees on one machine; events already carry per-node IDs from a hybrid logical clock. Entries that land never genuinely contradict each other. The ledger replays cleanly.
- **Regime B — multiple developers (or multiple worktrees) recording *conflicting* entries** into the ledger that then have to be reconciled and resolved before a clean replay is possible.

**Regime A is the degenerate case of Regime B where the conflict set is empty.** If the Regime-B machinery (detect → surface → human resolves → replay correctly) is built correctly, Regime A falls out for free. Therefore the partition is *not* "build single-dev now, defer multi-dev." It is: **build the full reconciliation machinery now; the only thing deferred is *automatic* conflict resolution.**

There is a second, orthogonal axis worth naming because it affects testing, not data model:

- **Self-dev:** Endless building Endless (the dogfooding case; the conflicts you hit across your own worktrees are the first test bed).
- **Project-dev:** Endless building any other project (code and conventions you do not control).

Both self-dev and project-dev exist today, and both span both concurrency regimes. The worktree-level concurrency of self-dev is a real, runnable test bed for Regime B *now* — git merge is git merge whether or not a second human exists — so Regime B is testable today, not hypothetical.

### What is built now vs. deferred (the explicit partition)

**Built now (must work for both regimes):**

- The full upcasting pipeline (declarative ops + imperative escape hatch), forward-only.
- Rebuild-as-migration (replay the project ledger into the read-model).
- The structural-vs-transform boundary and the single-rebuild interaction between them (§2).
- The two-ledger scope split (project vs. machine-user), enforced at emit time (§3).
- **Named + content-hash identity** (§5). Not deferred: project-dev across worktrees already produces concurrent authorship, so merge-safe identity is a now-requirement, and it costs almost nothing over integers while avoiding a painful later migration.
- **Atomic cross-kind grouping (migrations)** (§6). Not deferred: self-dev across worktrees already produces the forgotten-cross-kind-transform hazard.
- The **coverage linter** and the **tripwire** (§9).
- The full **conflict reconciliation path**: detect a conflict → surface it unmissably → the merger resolves it manually (exactly like a git merge conflict) → replay produces the correct result. Both collision sites are covered (§7): identity collisions (same `(kind, migration)` target — a git file conflict at merge time) and semantic collisions (files merge cleanly but transforms are incompatible against the same field — caught by the linter/tripwire/rebuild).

**Deferred on evidence, with a recorded trigger (see ADR-003):**

- ***Automatic*** conflict resolution only. The system never silently auto-merges conflicting transform entries. It surfaces; the human resolves. Automatic resolution waits until enough real conflicts have been observed to know which classes are safe to automate. This is deferred for lack of evidence about resolution policy, **not** because "only multi-dev needs it." Manual reconciliation is built now.

**YAGNI under both regimes today (do not build):**

- Bidirectional lenses / inverses (forward-only is sufficient; Part I §6, and §8 below). Reserve only if old readers must ever consume new events.
- A general schema-version *graph* with shortest-path between arbitrary versions. Endless's per-kind history is a **linear chain**; build the chain walk, not Dijkstra (§8).

---

## 1. What this package is

A dependency-free (standard-library-only) Go package implementing **forward-only, declarative, Cambria-style schema-evolution transforms ("upcasts")** for the Endless event-sourced system.

The system it serves:

- An **append-only event ledger** stored as **JSONL segments in git**, developed across multiple worktrees and (in Regime B) by multiple developers, then merged or rebased. The committed ledger is the source of truth for project-scope state.
- A **SQLite read-model** (`endless.db`) **rebuilt by replaying the project ledger**. The read-model is derived and disposable.
- When an **event payload's value or shape** changes (rename a status value, restructure a nested payload, split one event kind into several), the historical ledger is **not rewritten**; old events are **upcast into the current shape at replay time**.

This package provides the transform layer: declarative operations, the identity/merge model, the chain walk that takes any old event to current shape, the integrity checks, and the diagnosability primitives. It does **not** own the ledger reader, the SQLite projection, the structural-migration system, or the CLI. Keep SQLite and ledger I/O out of this package; it transforms events in memory and the host wires it in.

---

## 2. Boundary with the existing structural-migration system

Endless already has a structural-migration system, and the transform system must coexist with it. Define the boundary crisply.

**Two systems, two jobs:**

| | Structural migrations (existing) | Event-payload transforms (this package) |
|---|---|---|
| **Lives in** | `internal/schema/changes/*.{sql,go}` + always-latest `schema.sql` | `.endless/transforms/<kind>/…` (data files) |
| **Changes** | Table *shape*: new column, new table, FK-to-values-table, table rebuilds | Event *payload* value/shape: rename a value, restructure a payload, split a kind |
| **Applied** | **Once at land time**, recorded in `_schema_version` marker table | **At replay time, every rebuild**, against each old event |
| **Operates on** | The read-model database (the destination) | The events flowing from ledger into the projection (the source) |
| **Identity** | Per-ticket change file, applied-once marker | Named migration + per-kind entries with content hashes (§5, §6) |

**The rule of thumb:** if the change is about *what the tables look like*, it is a structural migration and it lives in `internal/schema/changes/` with `schema.sql` updated to match. If the change is about *what a historical event's payload means or how it is shaped* — so that replaying an old event would otherwise produce wrong or unparseable data — it is a transform and it lives in `.endless/transforms/`.

**Why they are different mechanisms, not one.** Structural migrations are *destination* edits applied once and marked; they are inherently in-place and ordinal (a `_schema_version` marker is a high-water mark). Transforms are *source* corrections applied on every replay and must survive concurrent authorship; they are inherently set-membership and content-addressed. Forcing either into the other's model reintroduces exactly the problems Part I catalogues — a transform applied "once" and marked would silently skip old events on a fresh rebuild; a structural ALTER expressed as a per-event transform would run N times instead of once.

### How the two interact during a single rebuild

A rebuild is triggered (§ on trigger below) and proceeds in a fixed order:

1. **Build the destination schema.** Apply the structural-migration system to produce an `endless.db` whose tables match the current `schema.sql`. (In practice this is "create from `schema.sql`"; the `internal/schema/changes/` files are the audit trail of how that shape was reached, and continue to be applied-once/marked for any live, non-rebuilt database.)
2. **Replay the project ledger through the transform pipeline into that schema.** Each event is walked to current payload shape by this package, then handed to the projection, which writes it into the structurally-current tables.

The ordering is not negotiable: the destination shape must exist before transformed events can be written into it. A transform that depends on a new column existing is only valid if the corresponding structural migration has already produced that column. **A transform never creates a table or column; it only reshapes event payloads so the projection can write them into tables the structural system already created.** If a logical change needs both — e.g. "move a field to a new table" needs a new table (structural) *and* the old events' payloads routed to populate it (transform) — it is authored as a *pair*: a structural change file and a transform migration, landed together. Document this pairing requirement; the coverage linter (§9) can check that a transform referencing a destination column has a corresponding structural change present.

---

## 3. The two-ledger scope split (project vs. machine-user)

Not all events belong in the committed, replayable ledger. The dividing axis is **scope — whose state the event is**, not durability. This distinction matters: a developer's focus history can be long-lived and valuable *to that developer* (persisted across restarts, queryable) while still being categorically wrong to commit to the shared project ledger, because it is *theirs*, not the *project's*. "Ephemeral" was a misnomer; the property is scope, and a machine-user event may be perfectly durable on its own machine.

Two scopes:

- **`project`** (`task.*`, `decision.*`, `epic.*`) — belongs to the project. Version-controlled, committed, replayed, merged, subject to the full transform/identity/conflict apparatus.
- **`machine-user`** (`session.*`, `conversation.*`, `message.*`, `note.*`, and — see below — `session_status.*`, `focus.*`) — belongs to this developer on this machine. Gitignored, never merged, never replayed-into-committed, outside the transform system. May still be durable locally; it is simply never shared.

A "muddy" third bucket exists today and is the thing to eliminate: `session_status.*` and `focus.*` are currently *written to the committed ledger but not replayed* — the worst of both, because they bloat and dirty version-controlled history with machine-user state that never reconstitutes any project state. They resolve to `machine-user` scope.

**The design: a hard physical split, enforced at emit time.**

```
.endless/
  db-ledger/              # project scope: committed, replayable. The root IS project scope.
    0001.jsonl
    0002.jsonl
    transforms/           # transforms apply ONLY to project-scope events
      <kind>/…
    migrations/           # migration manifests (§6)
      <migration-id>.json
  db-ledger/local/        # machine-user scope: GITIGNORED, never merged, never replayed-into-committed
    0001.jsonl
```

Note the directory asymmetry, and that it is deliberate: **project scope is the root** (`db-ledger/*.jsonl`), not a named `project/` subdirectory, because project scope is the default, unmarked location — the same way tracked files are just "the repo" and `.gitignore`'d files are the marked exception. **Machine-user scope is the marked opt-out** (`local/`, gitignored).

**Scope is a *required* declared property of each event kind, enforced at emit time.** Every event kind declares its scope (`project` or `machine-user`). The emit path routes by that declaration: a `project` kind physically writes to `db-ledger/`; a `machine-user` kind physically writes to `db-ledger/local/`. **A `machine-user` kind cannot reach the committed ledger** because the emit function will not write it there — a gate, not a convention.

**Scope must be declared, never defaulted — and here is why that is load-bearing.** The directory default and the safe-failure default point in *opposite* directions. The directory default is "the root," i.e. project scope; but the *safe* default for an undeclared kind would be to keep it *out* of version control (machine-user), because leaking machine-user state into the shared ledger is the harmful direction and is irreversible-by-rebuild (it is committed history). Because those two defaults conflict, the gate must treat scope as a **required declaration**: an event kind with no declared scope is a hard error at emit, not a silent fall-through to the root. Do not let "project is the root default" decay into "undeclared is fine."

**The muddy bucket is eliminated** by assigning each of its kinds a scope: `session_status.*` and `focus.*` become `machine-user`. The reframed edge case (cleaner than the prior "durable field inside an ephemeral kind" framing): if some field within one of these kinds genuinely describes the *project* rather than the *user's interaction with the project*, that field is project state and is split out into its own `project`-scope kind (a structural + transform pairing per §2). This is now a crisp scope question — "does this field describe the project, or the user's interaction with it?" — not a durability judgment. Flag any such case for human decision rather than guessing.

**Rebuild semantics with the split:**

- **Rebuild replays only the project ledger (`db-ledger/`) and swaps only the project-scope tables.** The structural rebuild produces a fresh project-table set; the project ledger is replayed (through transforms) into it; then those tables are swapped in (blue/green per Part I §4).
- **Live machine-user tables are untouched by rebuild.** Runtime state (current session, focus, open conversation) survives a rebuild because it lives in tables fed by `db-ledger/local/` and the live runtime, neither of which the project rebuild path reads or replaces.
- **Transforms apply only to project-scope events.** `db-ledger/local/` is never replayed-into-committed and never merged, so it needs no transform/identity machinery. (If local replay is ever wanted for developer convenience, it may reuse the same engine, but it carries no merge or conflict obligations.)

This makes the scope split a *correctness* boundary for the whole transform system: the entire identity/merge/conflict apparatus exists to serve `db-ledger/` (project scope), and `db-ledger/local/` (machine-user scope) is deliberately outside it.


---

## 4. The artifacts (do not conflate them)

Five distinct artifacts; conflating any two is a recurring failure mode.

1. **The project ledger** — `.endless/db-ledger/*.jsonl`. Append-only, committed, replayable, project scope. Events only. Each event records the migrations its writer had already applied (§5). Never rewritten in place except by the deliberate, audited baseline operation (§10).
2. **The local ledger** — `.endless/db-ledger/local/*.jsonl`. Machine-user scope: gitignored, developer-specific, never merged, never replayed-into-committed. Outside the transform system.
3. **`endless.db`** — the live SQLite read-model. Project-scope tables rebuilt by replay; machine-user tables live and untouched by rebuild. Disposable (the project-scope half). This package does not touch it.
4. **`schema.sql`** — the always-latest DDL. The `schema.sql` in any commit is the DDL that produces an `endless.db` matching that commit's code; it carries no history, no versions, no per-event variants. It is the destination transforms walk toward, owned by the structural-migration system, not this package.
5. **The transform/migration set** — `.endless/db-ledger/transforms/<kind>/…` and `.endless/db-ledger/migrations/<id>.json`. Version-controlled data, merged like code, consulted at replay. This package's own data.

---

## 5. Identity: named migrations grouping per-kind entries, each content-hashed

This section supersedes the prior "per-(event-type, name)" identity model, integrating Point 1's grouping. The conclusion, in one line: **a logical schema change is a named *migration* that groups N per-kind transform entries; identity is the migration name plus a content hash, and each event records the set of migrations applied to it.**

The reasoning for grouping (Point 1) is in §6. The identity mechanics:

**Three identity fields, three jobs (unchanged from prior version, now at the migration grain):**

| Field | Job |
|---|---|
| **migration name** (e.g. `e-1532-status-rename`) | Identity + merge. Human-controlled, stable, legible, ticket-derived. What git keys on (the manifest file path) and what set-membership is computed from. |
| **content hash** (over the canonical form of the migration: its manifest + all per-kind entries) | Integrity + dedup. Two *different* migrations can never claim the same identity; a migration's recorded hash changes if any of its entries change. |
| **description** (free text) | Documentation. |

**Sequential integers remain rejected** (Part I §2): two worktrees each authoring "migration 7" collide on merge and force renumbering. Ticket-derived names (`e-1532-…`) are naturally unique across branches and need no global counter.

**What an event records.** Each project-scope event records the **set of migrations applied to it**, as name + hash:

```json
{
  "kind": "task.status_changed",
  "applied": ["e-1532-status-rename@a1b2c3d4", "e-1601-priority-enum@d4e5f6a7"],
  "data": { "...": "..." }
}
```

- `applied` is a **set** (membership semantics), never an ordered list with positional meaning. Authoring order across branches is not globally agreed; do not depend on array order.
- An event records a migration by the migration's identity (name + hash), **not** by which per-kind entry within it applied. Which entry applied to *this* event is derivable: it is the entry for this event's `kind`. This keeps the recorded set small and at the logical-change grain.
- An empty `applied` set means the event is in the base shape for its kind.

**Hashing specifics:** hash the **canonical form** (sorted keys, normalized whitespace) of the migration — manifest plus all per-kind entries — so cosmetic edits do not change the hash (mirrors Avro Parsing Canonical Form). Use `crypto/sha256`; store the full hash, display a short prefix (`e-1532-status-rename@a1b2c3`).

---

## 6. Point 1 resolved: atomic cross-kind grouping (the migration construct)

**Recommendation: a hybrid — a named *migration* that groups per-kind entries, where the manifest is a thin grouping/reference layer over per-kind entry files.** This is built now (it is a both-regimes requirement, not a multi-dev-future item). The reasoning, addressing each question the team raised:

### Why grouping at all

A single logical change usually spans multiple event kinds. `needs_plan` → `unplanned` must transform `task.created`, `task.fields_updated`, **and** `task.status_changed` payloads. Under bare per-(kind, name) files, that one logical change is three independent files, and the only thing preventing a forgotten kind (transform two of three, silently corrupt the third on replay) is the coverage linter catching an omission after the fact. Grouping makes the cross-kind coupling **structural**: the migration *is* the unit, and "which kinds must this migration touch" is a declared, checkable property rather than something the linter must infer.

### Does grouping break clean git-merge? No — if the manifest is a reference layer, not a monolith.

This is the crux, and it determines the file layout. Two ways to group:

- **Monolithic migration file** (all per-kind entries inline in one JSON). *Rejected*: it makes the migration a coarse conflict unit. Two developers editing entries for *different* kinds within the same migration would conflict on the one file even though their changes are independent. That throws away the union-merge property Part I valued.
- **Manifest-of-references** (*recommended*): the migration is a small manifest listing the kinds it covers and referencing per-kind entry files; each per-kind entry is its own file.

```
.endless/db-ledger/
  migrations/
    e-1532-status-rename.json        # manifest: name, description, covers:[kinds], entry refs+hashes
  transforms/
    task.created/
      e-1532-status-rename.json      # the per-kind entry (ops for this kind)
    task.fields_updated/
      e-1532-status-rename.json
    task.status_changed/
      e-1532-status-rename.json
```

With the manifest-of-references layout, the merge story is **strictly better than bare per-kind files, not worse**:

- Two migrations touching *different* kinds → disjoint file sets → clean union merge. (Same as before.)
- Two developers extending the *same* migration to *different* kinds → they each add a different per-kind entry file and both touch the manifest's `covers` list. The entry files merge cleanly (different paths); the manifest's `covers` list is the one small shared edit, and a list-of-strings merge is about the most benign git merge there is (and if it does conflict, it conflicts on a 1-line list, trivially resolved).
- Two developers editing the *same* per-kind entry of the *same* migration → conflict on that one small file. This is the *correct* conflict: they changed the same target. (Same-target-conflict-only property preserved.)

So grouping does **not** coarsen the conflict unit, provided the group is a manifest of references rather than a monolith. The conflict unit stays at the per-kind-entry grain; the manifest adds a cheap, mostly-conflict-free coordination layer.

### Atomicity: is "all entries apply or none" a real gain? Partly — and not the part you'd expect.

Under rebuild-from-log, **partial application at replay is already impossible**: replay applies the *whole current transform set* every time, so there is no "applied entry 1 and 2 but not 3" runtime state to get stuck in. The team's atomicity intuition, read as a *replay-time* guarantee, is already provided by rebuild-as-migration and grouping adds nothing there.

Where grouping's atomicity *does* help is **authoring and merge**, not replay:

- **Authoring:** the migration declares `covers: [task.created, task.fields_updated, task.status_changed]`. The linter checks every covered kind has an entry and every kind carrying the affected field is in `covers`. A forgotten kind is caught as a *missing declared entry*, which is a decidable check, rather than the linter having to infer cross-kind coupling from field names. (See §9 — grouping is what makes the coverage linter's job decidable instead of heuristic.)
- **Merge:** the logical change moves as a unit. You cannot land a branch that has the `task.created` entry but, through a botched cherry-pick, dropped the `task.status_changed` entry, because the manifest references it and the reference's hash will not resolve — a hard error at load (§7).

So: atomicity is a real gain, located at author/merge time, enforced by the manifest+linter+hash, not a replay-time gain (replay already can't half-apply).

### Diagnosability: is structural grouping strictly better than per-kind-files + hard-gate linter?

They are **complementary, and the combination is what you want** — grouping does not replace the linter, it makes the linter *decidable*:

- Without grouping, the linter must answer "does every kind whose payload carries field X have a transform for the change to X?" by scanning payloads and inferring intent — heuristic, prone to false negatives on fields it cannot statically associate.
- With grouping, the migration *declares* `covers`, so the linter answers two crisp questions instead: (1) is every kind in `covers` backed by an entry file whose hash matches the manifest? (decidable, structural) and (2) does any kind *outside* `covers` carry the affected field? (the heuristic part, but now only a backstop, and a false negative here is a missing-`covers`-entry the author can be prompted to confirm rather than a silent corruption). Structural grouping converts most of the linter's job from inference to a hash/membership check, and narrows the irreducibly-heuristic part to a single "did you mean to also cover kind Y?" prompt.

So: **grouping + linter, where grouping makes the linter mostly decidable.** Neither alone is as good. This is exactly the "diagnose, don't depend on remembering" requirement: the structure encodes the expectation, the linter checks it cheaply.

### Identity/merge of a grouped migration without reintroducing ordering/collision

Covered in §5: the migration carries identity as **name + content hash** (hash over manifest + all entries), recorded in each event's `applied` *set*. No ordinal, no global counter, so no renumbering on merge. Two same-named migrations from two branches are a git file conflict on the manifest path (correct — same logical change authored twice, reconcile once). Different-named migrations union cleanly. The set-membership recording means an event that has been through both `e-1532` and `e-1601` simply lists both; order of authoring is irrelevant, and cross-migration ordering is handled by declared dependencies + canonical tiebreak exactly as in §8.

---

## 7. The two conflict-collision sites (built-now reconciliation, deferred auto-resolution)

"Build for both regimes" requires the system to *handle conflicting entries that land and must be reconciled*. There are exactly two sites where conflicting transform entries collide, they surface at different moments, and both are handled now by **detect → surface → human resolves → replay correctly**. Only *automatic* resolution is deferred (ADR-003).

**Site 1 — identity collision (same target).** Two branches author an entry at the same path: `transforms/task.status_changed/e-1532-status-rename.json`, or the same migration manifest. **Surfaces at merge time as a git file conflict.** This is the desired behavior: same logical target edited twice, git stops the merge, the merger resolves it by hand like any code conflict. No special machinery needed beyond keeping entries in small, single-purpose files so the conflict is legible. Build-now obligation: file layout that makes these conflicts small and on-target (done — manifest-of-references, one file per per-kind entry).

**Site 2 — semantic collision (clean file merge, incompatible meaning).** Two branches author *differently-named* migrations that both transform the same field of the same kind in incompatible ways (e.g. `e-1532` maps `needs_plan`→`unplanned` while `e-1540` maps `needs_plan`→`backlog`). The files merge cleanly (different paths), so git is silent. **Surfaces at the tripwire and at rebuild, not at merge.** Build-now obligation: the **coverage linter and tripwire (§9) must detect that two applied migrations write the same (kind, field) with divergent results** and surface it as a hard error naming both migrations, for the merger to resolve (by editing one, or by adding a reconciling migration with a declared `after` dependency). This is the reconciliation path the dual-regime scope requires, and it is built now.

**Deferred (ADR-003):** having the system *choose* which of the two semantic transforms wins. Until there is lived experience with real semantic conflicts, the system surfaces and the human resolves. It never silently auto-merges.

This two-site model is the concrete content of "build for both": Regime A simply never hits either site; Regime B hits them and the machinery above carries it from detection to a correct post-resolution replay.

---

## 8. Forward-only, and the replay chain walk

**Forward-only (locked).** Transforms migrate old events toward current shape; inverses are not required. The system replays into one current read-model and never needs old code to read new events. Forward-only is simpler and admits operations that are not valid bidirectional lenses (one-way value conversion, lossy conversion, lookups, N→M splits). If forward compatibility is ever needed, inverses would be retrofitted — a known, deliberately-closed door. (Part I §6.)

**Linear chain, not a graph (YAGNI guard).** Cambria walks a schema *graph* by shortest path because its documents can sit at arbitrary versions. Endless's per-kind history is a **linear chain** of migrations in dependency order. Build the chain walk: for an event, the migrations to apply are those in the kind's chain that are **not** already in the event's `applied` set, applied in dependency order. Do not build general-graph shortest-path; it is unjustified complexity under both regimes.

**Ordering across concurrently-authored migrations** (the merge-safe ordering, built now for Regime B):

1. **Declared dependencies** — a migration may declare `after: [<migration-name>]`; the engine topologically sorts. Explicit and merge-safe. Use this whenever two migrations genuinely must order (e.g. a reconciling migration from §7 Site 2).
2. **Deterministic canonical tiebreak** — absent a declared dependency, apply in a stable content-derived order (e.g. by migration name) so every machine computes the same order. A tiebreak, not a correctness guarantee for order-dependent changes.

Genuinely order-dependent migrations **must** declare `after`; the engine cannot infer it. Independent, commuting migrations (the common case) need no dependency and merge as a clean union.

**Integrity gate.** Before applying any migration to an event, verify the migration's current content hash matches the hash recorded in the event's `applied` set, and verify every entry the manifest references resolves to a present file with a matching hash. Mismatch = hard failure naming the migration and both hashes. (This is what makes the §6 "can't land a half-cherry-picked migration" guarantee real.)

---

## 9. Diagnosability: coverage linter + tripwire (first-class, built now)

The requirement: *the system diagnoses; it does not depend on a human or AI remembering.* Two instruments, at two cadences.

**The coverage linter (authoring/land-time depth).** When a migration transforms a value in field X, it must cover every event kind whose payload carries X. The linter:

1. **Decidable structural checks (from grouping, §6):** every kind in the migration's `covers` list has an entry file present whose hash matches the manifest; every referenced entry resolves. Fail hard on any mismatch.
2. **Cross-kind coverage backstop (the heuristic part, now narrowed):** scan every event kind whose payload schema carries field X; for any such kind **not** in `covers`, surface "kind Y carries field X but migration e-NNNN does not cover it — intended?" as a hard prompt the author must resolve (add the entry, or explicitly annotate why Y is exempt). Grouping is what shrinks this from "infer all coupling" to "check the declared set and flag the residue."
3. **Semantic-collision check (§7 Site 2):** flag two applied migrations writing the same (kind, field) with divergent results.
4. **Structural-pairing check (§2):** flag a transform whose entries reference a destination column with no corresponding structural change present.

**The tripwire (frequent, cheap, runs on each agent prompt).** A fast invariant check that surfaces drift immediately, reserving the full projection-diff for land time:

- every project-scope event's `applied` set ⊆ the migration registry (no event references a migration that does not exist — catches a dropped/renamed migration after a bad rebase);
- no machine-user kind present in the committed `db-ledger/` (the emit-time gate's runtime audit — catches a regression in routing, i.e. machine-user state leaking into project scope);
- row-count sanity between ledger and read-model (counts line up for project-scope kinds);
- registry integrity (every manifest's referenced entries resolve with matching hashes).

The tripwire is O(cheap) — set membership, a routing scan, count comparisons — so it can run on every agent prompt without friction. The **full projection diff** (rebuild into a scratch DB and compare to live) is heavier and runs at land time. The division is deliberate: the tripwire catches the *structural* drift that makes replay wrong (a missing migration, a leaked machine-user kind) in milliseconds; the land-time diff catches *semantic* drift (the rebuild produces different rows) when you can afford it.

---

## 10. Rebuild trigger

**Rebuild runs only on a land-or-pull that ADDS a migration.** The system detects a new migration manifest arriving in the merge/rebase and rebuilds as part of its existing land/rebase step. **No git hook.** Normal work-lands that add no migration do not rebuild. Mechanism: compare the migration set before and after the land/pull; a non-empty addition triggers the project-scope rebuild (§3 semantics — project-scope tables only, machine-user untouched). A removed or changed migration (hash mismatch on an existing name) is a tripwire-level hard error, not a silent rebuild, because it implies history was edited (see baseline, below).

---

## 11. Compaction / baselining: retiring old migrations

Migration chains grow without bound; ancient events need a long walk on every rebuild. Bound it with a **baseline**, analogous to Kafka log compaction and aggregate snapshotting (Part I §7).

**Mechanism:** periodically read the whole project ledger, upcast every event to current shape, and write a **new baseline segment already in current shape**, marking prior segments superseded. Once the baseline absorbs all events at or below a migration's input position, **that migration can be retired** (no live post-baseline event still records it in `applied`).

**Trigger (host policy; this package provides the primitive):** full project-ledger-replay wall-clock exceeds budget; or chain depth for the oldest event exceeds a small bound; or a migration is older than N releases.

**Retire a migration only when** no live post-baseline event records it. Keep a recorded manifest of which migrations the baseline folded in.

**Immutability tradeoff (state honestly):** baselining rewrites history, contradicting strict append-only. Keep it honest: it is a **deliberate, reviewed, git-committed operation** appearing as a normal commit/PR; pre-baseline segments remain in git history so the baseline is re-derivable and auditable; never edit segments in place — write new baseline segments and mark old ones superseded. This is also why §10 treats a changed-hash-on-existing-migration as a hard error: the *only* sanctioned way history changes is an audited baseline, so an unexplained hash change is drift to surface, not to absorb.

---

## 12. Suggested package shape (Go)

Standard library only. A sketch of load-bearing types, not a mandate.

- **`Op`** — one declarative operation (`rename`, `add`, `remove`, `hoist`, `plunge`, `wrap`, `head`, `in`, `map`, `convert`, `custom`), dispatched by a registry/switch keyed by `op` name so the set is extensible (promote-a-primitive). *Staging:* `in`/`map` (nested/array sub-lens ops) are fiddly and not needed by the first migrations; ship the flat ops plus `custom` first.
- **`Entry`** — the per-kind transform for one kind within one migration: `Kind`, `Ops`, `Hash`.
- **`Migration`** — a named group: `Name`, `Description`, `Covers []Kind`, `Entries map[Kind]Entry` (loaded from the referenced files), `After []string`, `Hash` (over canonical manifest + entries).
- **`Registry`** — all migrations, loaded from `.endless/db-ledger/migrations/*.json` + `transforms/<kind>/*`. Keyed by migration name. Computes, for an event, the ordered set of migrations to reach head; verifies hashes.
- **`CustomRegistry`** (escape hatch) — `map[string]func(event) ([]event, error)` (slice return allows N→M splits), host-populated before replay. Unregistered custom op at replay = hard error.
- **Replay/walk function** — event + Registry → upcast event(s) at current shape, after hash verification and dependency-sorted application. Returns a slice (custom/split can produce multiple events).
- **Linter + tripwire** — the §9 checks, as callable functions the host runs at land time (linter, full check) and per-prompt (tripwire, cheap subset).
- **Baseline helper** — replay-to-current-shape, emit baseline segment, report retire-able migrations.
- **Canonicalize + hash helpers** — canonical JSON form and content hash.

**Hard rules:**

- **No third-party dependencies.** Standard library only.
- **No SQLite, no ledger file I/O beyond loading this package's own transform/migration data, no CLI** inside the package. It transforms events in memory; the host wires ledger and projection.
- **Forward-only.** No inverse machinery. **Linear chain walk**, not graph shortest-path.
- **Hash mismatch, unregistered custom op, missing referenced entry, semantic collision, and leaked-machine-user-kind are all hard errors**, never silent skips.
- **Set semantics for `applied`** — never depend on array order; order from `after` + canonical tiebreak.
- **Machine-user scope never enters the transform system** — `db-ledger/local/` is outside this package's obligations.
- Follow the host project's Go style conventions (in the implementing session's context; not restated here).

---

## 13. One-paragraph summary for the implementing session

Build a standard-library-only Go package that upcasts project-scope JSON events from whatever shape their writer produced to the current shape, forward-only, via a linear per-kind chain of **named migrations** that **group per-kind declarative transform entries** (`rename`, `add`, `remove`, `hoist`, `plunge`, `wrap`, `head`, `in`, `map`, `convert`) plus a host-registered imperative escape hatch. A migration is a manifest-of-references (small manifest + one file per per-kind entry) so concurrent authorship merges as a clean union and conflicts only on same-target edits; identity is the migration name plus a content hash over the canonical manifest+entries, never a sequential integer. Each project-scope event records the **set** of migrations applied to it (name + hash, set semantics); replay verifies hashes and applies missing migrations in dependency-sorted order, returning a slice to allow N→M splits. The package serves only the committed project ledger (`.endless/db-ledger/`); machine-user-scope kinds are physically routed to a gitignored `db-ledger/local/` at emit time (scope is a required declared property of every event kind) and never enter the transform system; rebuild swaps only project-scope tables and leaves live machine-user state untouched. Event-payload value/shape changes are transforms (this package); table-shape changes are structural migrations (`internal/schema/changes/` + `schema.sql`), and a rebuild first builds the structural destination then replays transformed events into it. Diagnosability is first-class: a coverage linter (made mostly decidable by grouping's `covers` declaration) and a cheap per-prompt tripwire catch forgotten-kind, leaked-machine-user, dangling-migration, and semantic-collision errors, surfacing them for the merger to resolve by hand exactly like a git conflict — the system never auto-resolves. Rebuild triggers only when a land/pull adds a migration. Provide a baseline primitive to retire old migrations, treated as a deliberate, git-committed, auditable history rewrite.

---

# Part III — Decision Records (Revised)

Decision records capture *why* a choice was made, the alternative rejected, and the symptom that would justify revisiting it. If you are reading one because you hit the failure it describes, it tells you whether you are looking at a bug or an accepted tradeoff.

## ADR-001 (revised): Custom transforms — declaration hash plus mandatory golden-fixture tests, not body hashing

**Status:** Accepted (revised — Point 2). Supersedes the prior ADR-001, which recommended declaration-only hashing closed by a "never edit in place" convention.

**Context.** Declarative transforms hash over their JSON definition, so any behavior change trips the replay-time integrity gate. The imperative escape hatch (`custom` op) is split between a JSON declaration ("run function `X` with these params") and the Go function `X` in source. The question: what does the hash cover, and how is a behavior change in `X` caught?

**Decision.** Hash **only the JSON declaration** (which function, which params) for the replay-time integrity gate, **and additionally require a golden-fixture CI test for every custom transform**: recorded input event(s) → expected output event(s). A behavior change in `X` fails the golden-fixture test at CI, not at replay.

**Why not also hash the function body (the rejected middle option).** Source-text hashing is decisively wrong in both directions at once, which is why it is rejected rather than merely imperfect:

- **Over-sensitive:** reformatting, comment edits, or a local variable rename change the body bytes and would trip a **hard replay failure** on a behavior-preserving edit. A hard gate that fires on cosmetic edits trains people to bypass it — the worst property a gate can have.
- **Under-sensitive:** a behavior change in a *helper that `X` calls* leaves `X`'s own body bytes unchanged, so a body hash stays silent on a real behavior change. The gate misses exactly the case it was added for.

A gate that both false-alarms on safe edits and stays silent on unsafe ones is worse than no gate, because it manufactures false confidence. This objection is decisive on its own.

**Why golden fixtures are strictly better than the convention alone.** The prior ADR-001 closed the gap with a discipline ("never edit an in-use custom transform; write a new one") and accepted that the forgetful are uncovered. Point 2's question — "if the discipline is mandatory anyway, why not cover the forgetful too?" — is answered by golden fixtures, which cover the forgetful **without** coupling replay to source and **without** false-positiving on cosmetic edits:

- A behavior change in `X` (direct or via a helper) changes the output for the recorded input, so the golden-fixture test fails. This catches the under-sensitive case that body-hashing misses.
- A cosmetic edit to `X` does not change the output, so the test passes. This avoids the over-sensitive false alarm that body-hashing causes.
- The check runs at **CI**, the correct place for "did this code change behavior," rather than at **replay**, where a source-coupled check does not belong (the package transforms events in memory and otherwise touches no source).

**What each mechanism is responsible for, after this decision:**

- **Declaration hash (replay-time gate):** catches "this event was written against a *differently-declared* custom transform" — a changed function name or params. Stays silent on body edits, which is now correct, because:
- **Golden-fixture tests (CI gate):** catch "the behavior of this custom transform changed." This is the gate for body behavior.
- **The convention** ("new behavior → new migration name") remains good practice and keeps the recorded `applied` history meaningful, but it is now backstopped by CI rather than being the sole protection.

**Why this fits Endless's principles.** Gates over guidelines: the golden-fixture test is a gate (CI fails), replacing a guideline (the convention) that the prior ADR leaned on alone. It is also the cheapest gate that is neither over- nor under-sensitive, and it is justified under both concurrency regimes today (a single developer editing a custom transform across worktrees benefits identically to a team).

**Symptom that would justify revisiting.** If custom transforms become numerous and golden-fixture maintenance becomes a burden, or if a class of custom transform proves hard to fixture (non-deterministic output, external dependency the fixture cannot capture), revisit — likely by constraining custom transforms to be pure functions of their input event(s) (which they should be anyway) so they are always fixturable. If a custom transform cannot be made a pure function of its input, that is the real defect to fix, not the hashing policy.

## ADR-002 (new): Migration as manifest-of-references, not monolith — grouping without coarsening the conflict unit

**Status:** Accepted (Point 1).

**Context.** A logical schema change spans multiple event kinds (a status-value rename touches `task.created`, `task.fields_updated`, `task.status_changed`). The choice was: bare per-(kind, name) files relying on the coverage linter to catch a forgotten kind, versus a grouped migration construct making cross-kind coupling structural.

**Decision.** Adopt a grouped **migration** construct, implemented as a **manifest-of-references**: a small manifest (`name`, `description`, `covers`, entry references + hashes) plus one file per per-kind entry. Identity is the migration name + a content hash over manifest and entries; events record the **set** of applied migrations.

**Why manifest-of-references and not a monolithic migration file.** A monolith (all entries inline) makes the migration a coarse conflict unit: two developers editing entries for *different* kinds within the same migration would conflict on the one file despite independent changes, discarding the union-merge property. The manifest-of-references keeps the conflict unit at the per-kind-entry grain (clean union when different kinds/migrations; same-target conflict only when the same entry is edited twice) while adding only a small, mostly-conflict-free `covers` list as the coordination point.

**Why grouping at all (over bare per-kind files + linter).** Grouping makes cross-kind coverage **decidable**: the migration declares `covers`, so the linter checks "every covered kind has a hash-matching entry" structurally, and narrows the irreducibly-heuristic part to a single "kind Y also carries this field — cover it?" prompt. It also makes a half-applied logical change (a botched cherry-pick dropping one entry) a **hard load error** (the manifest's reference will not resolve), rather than a silent corruption the linter must notice. The two mechanisms are complementary: grouping encodes the expectation structurally; the linter checks it. Neither alone is as strong.

**On atomicity.** "All entries apply or none" is **not** a replay-time guarantee that grouping provides — rebuild-from-log already applies the whole current set every time, so partial application at replay is already impossible. Grouping's atomicity benefit is at **author and merge time**: you cannot land a partial logical change, and the linter checks the group is complete. State this so no one expects grouping to fix a replay-partial-application problem that does not exist.

**Symptom that would justify revisiting.** If migrations routinely cover only one kind (cross-kind changes turn out rare in practice), the manifest layer is overhead; a single-entry migration is still fine but the grouping ceremony could be relaxed. Conversely, if the `covers` list becomes a frequent conflict point, reconsider how that coordination is recorded (e.g. derive `covers` from the present entry files rather than declaring it). Neither is expected; the manifest-of-references is cheap under both regimes.

## ADR-003 (new): Conflict reconciliation is built now; automatic resolution is deferred on evidence

**Status:** Accepted (Point 3 / scope).

**Context.** Endless must be correct under two concurrency regimes now: single-developer-no-conflicts (Regime A) and multiple-developers-recording-conflicting-entries-that-must-be-reconciled (Regime B). Regime A is the empty-conflict-set case of Regime B. The question is what to build now versus defer.

**Decision.** Build the **full conflict reconciliation path now** — detect a conflict, surface it unmissably, let the merger resolve it by hand (exactly like a git merge conflict), and replay correctly once resolved — covering both collision sites (identity collisions at merge time via git file conflicts; semantic collisions at tripwire/rebuild via the linter). **Defer only *automatic* conflict resolution** (the system choosing which side wins), with a recorded trigger.

**Why manual reconciliation is a now-requirement, not a deferral.** Regime B is not a hypothetical future user; it is a concurrency regime the system encounters today, including in self-dev across worktrees. If conflicting entries can land — and they can — the system must carry the user from a conflicting state to a correct replay. The machinery for that (detection, surfacing, hash gates that refuse to replay an inconsistent set, the linter's semantic-collision check) is therefore built now. Regime A gets it for free as the degenerate case.

**Why automatic resolution is deferred.** Choosing which of two incompatible transforms wins is a policy decision with no good default and no current evidence base. Building it now means inventing resolution rules before observing which conflicts actually occur and which resolutions are safe — guessing at policy, then encoding the guess as a gate, which is precisely the failure mode of a guideline-dressed-as-a-gate. The correct and sufficient behavior until then is the git model: surface, and the merger resolves. This is a deferral on **evidence grounds** (we lack data to choose a resolution policy), explicitly **not** on scope grounds ("only multi-dev needs it") — almost nothing in this design is deferred for being multi-dev-only, because project-dev and self-dev already span both regimes.

**The deferral's trigger.** Begin building automatic resolution only after enough real semantic conflicts have been observed and hand-resolved to identify a *class* whose resolution is mechanical and safe (e.g. "two migrations adding disjoint defaults to the same payload always compose"). Automate that class, leave the rest manual, and never let automation downgrade a genuine conflict to a silent guess. Until a class is demonstrably safe, every semantic conflict is surfaced for human resolution.

**Symptom that would justify revisiting.** If hand-resolution of a recurring, clearly-mechanical conflict class becomes a frequent annoyance with an obvious correct answer, that class is a candidate for automation per the trigger above. Conversely, if surfaced conflicts turn out vanishingly rare in practice (the likely case in self-dev), automatic resolution stays unbuilt indefinitely with no cost — which is the intended outcome of deferring it.