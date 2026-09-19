# Research: event-sourcing schema evolution & upcasting prior art

This task's "plan" is the prompt(s); its outcome is the latest (current best)
report. The report is revised in rounds; each round's prompt is recorded below for
posterity, and the outcome holds the most recent report (prior reports are
recoverable from ledger/git history).

---

## Round 1 prompt (run on Claude Web Research, 2026-06-26)

```
I'm designing an event-sourcing "upcasting" system and want to know the prior art
before committing to a design. Context: an append-only event log (JSONL segments)
is the source of truth; a SQLite read-model is rebuilt by replaying the log. When
the schema or a stored value changes (e.g. renaming a status value, or moving a
field to a new table), I do NOT want to rewrite the historical log — instead, old
events should be transformed ("upcast") into the current shape at replay time.

Unusual constraint: the event log is stored IN GIT and developed by multiple people
working independently/offline, then merged/rebased — so schema evolution happens
concurrently on different branches and must merge without ceremony, like code.

Please research and compare how mature systems handle this, with concrete specifics:

1. Event schema evolution / upcasting patterns in established frameworks:
   Axon Framework (event upcasters, revision numbers), EventStoreDB, Marten (.NET),
   Eventuous, NEventStore, Kafka + Confluent Schema Registry, Apache Avro/Protobuf
   schema evolution rules. What is each one's versioning unit — per-event-type
   revision integer, global version, named migrations, or schema fingerprint?

2. Versioning IDENTITY specifically: sequential integers per event type vs. a SET of
   applied named migrations (Flyway / Rails schema_migrations style) vs. content-
   addressed/hash schema IDs. Which survives concurrent, independently-authored
   changes that get merged later, without a global counter that must be renumbered?

3. "Weak schema" / tolerant-reader vs. explicit upcasting — when does each break down?
   Which non-additive changes (rename, remove, restructure, split-into-new-entity)
   force explicit transforms?

4. Forward-migrating a live read-model vs. replay-time upcasting: do systems UNIFY
   these (treat "migrate the DB" as "rebuild the projection") or keep them separate?
   Pros/cons. Any notion of rebuild-as-migration.

5. Local-first / distributed-log systems where the log itself is merged across nodes:
   Martin Kleppmann's work, Automerge, Ink & Switch essays, CRDT event logs, Git-based
   data stores (Dolt, irmin). How do they handle schema/transform evolution under merge?

6. Declarative transform libraries: systems that express upcasts as DATA (rename-field,
   rename-value, set-default, route-to-new-stream) rather than imperative code, plus an
   escape hatch. Any "promote a repeated pattern to a primitive" precedent.

7. Compaction/snapshotting to retire old upcasters (fold history into a new baseline
   segment) — how it's triggered and the immutability tradeoffs.

For each, give the concrete mechanism, a short example, and the failure modes. End with
a recommendation for the git-merged, multi-developer, rebuild-as-migration case above.
```

---

## Round 2 prompt — revise Parts II & III (run on Claude Web Research, 2026-06-27)

```
# Follow-up to your event-sourcing upcasting report — please revise Parts II & III

This is a follow-up to your prior report ("Schema Evolution & Upcasting for
Event-Sourced Systems… for the Git-Merged JSONL Log (Endless)"), which had
Part I (survey), Part II (implementation brief), Part III (ADRs). The design team
has reviewed it and largely accepts Part I and the core of Part II. Below is
context the original prompt did NOT give you, plus three points to reconsider.
Please produce a revised Part II and Part III that account for all of it.

## Context the first prompt omitted (please integrate)

The target system is **Endless**, and these specifics matter:

1. **There is already a structural-migration system.** Per-ticket change files live
   in `internal/schema/changes/*.{sql,go}`, applied once at land time, recorded in
   a `_schema_version` marker table; `schema.sql` is the always-latest DDL. These
   handle *table-shape* changes (new column, new table, FK-to-values-table, table
   rebuilds). Your transforms-as-data-files proposal must coexist with this.
   **Define the boundary crisply:** which changes go to `internal/schema/changes/`
   + `schema.sql` (structural) vs. the new `.endless/transforms/` (event-payload
   value/shape upcasts), and how the two systems interact during a single rebuild.

2. **Two-ledger ephemeral split (a requirement, not yet in your design).** Not all
   events should be in the version-controlled, replayable ledger. Three buckets
   exist today:
   - durable (task.*, decision.*, epic.*) — committed + replayed;
   - ephemeral (session.*, conversation.*, message.*, note.*) — runtime-only;
   - a "muddy" bucket (session_status.*, focus.*) currently WRITTEN to the
     committed ledger but NOT replayed — the worst of both.
   The design: split into `.endless/db-ledger/` (committed, durable, replayable)
   and `.endless/db-ledger/local/` (**gitignored**, developer-specific, never
   merged). An event kind's bucket is a declared property enforced at emit time so
   an ephemeral kind physically cannot reach the committed ledger. Please fold this
   into the data model and the rebuild semantics (rebuild swaps only durable
   tables; live ephemeral tables are untouched so runtime state survives).

3. **Diagnosability is a first-class requirement** ("the system should diagnose,
   not depend on a human or AI remembering"). Specifically: (a) a **coverage
   linter** — when a transform renames a value in field X, scan every event kind
   whose payload carries X and flag any kind lacking a corresponding transform;
   (b) a cheap **tripwire** that runs frequently (on each agent prompt) checking
   ledger/registry invariants (every event's applied-set ⊆ the registry; no
   ephemeral kind in the committed ledger; row counts line up) and surfaces drift
   immediately, with the full projection-diff reserved for land time.

4. **Rebuild trigger.** Rebuild runs only on a land-or-pull that ADDS a transform;
   the system detects the new transform file in the merge and rebuilds as part of
   its existing land/rebase step (no git hook). Normal work-lands don't rebuild.

## Point 1 — argue against (or for) ATOMIC CROSS-KIND GROUPING

Your Part II keys transforms per `(event-type, name)`. The team's concern: **a
single logical schema change usually spans multiple event kinds.** A status-value
rename (`needs_plan`→`unplanned`) must transform `task.created`,
`task.fields_updated`, AND `task.status_changed` payloads. Under per-(event-type,
name) files, that one logical change becomes three independent files, and the ONLY
thing preventing a forgotten kind (transform `task.created` but not
`task.status_changed`, silently corrupting replay) is the coverage linter.

**The team's proposed alternative:** a named **migration** construct that GROUPS
the N per-kind transform entries as one unit, applied atomically — so cross-kind
coupling is *structural* (you can't half-apply a logical change), not dependent on
a linter catching an omission. One migration `e-1532-status-rename` contains the
three per-kind entries.

Please evaluate this honestly, addressing:
- Does grouping break the clean git-merge property you valued (union of files,
  conflict only on same-target edits)? Can a grouped migration still merge cleanly,
  or does the group become a coarse conflict unit?
- Partial-application / atomicity: is "all entries of a migration apply or none" a
  real safety gain, or does rebuild-from-log already make partial application
  impossible (you always replay the whole set)?
- Diagnosability: is structural grouping strictly better than a per-kind-files +
  hard-gate coverage-linter combination, or are they equivalent in catching the
  forgotten-kind error?
- Identity/merge: how does a grouped migration carry identity (name + content
  hash) and record itself in each event's applied-set without reintroducing the
  ordering/collision problems you warned about?
Recommend: grouped-migration construct, or per-kind files + hard-gate linter, or a
hybrid — with the reasoning.

## Point 2 — reconsider ADR-001 (custom-transform hashing)

ADR-001 hashes custom (escape-hatch) transforms by their JSON *declaration*, not
the Go function body, accepting that an in-place body edit changes behavior
without tripping the integrity gate, closed by the convention "never edit an in-use
custom transform; write a new one."

The team asks: if the discipline is mandatory anyway, why not ALSO hash the body so
the forgetful are still covered? Please address:
- The objection that source-text hashing is **over-sensitive** (reformatting,
  comments, local renames trip a HARD replay failure on behavior-preserving edits)
  AND **under-sensitive** (a behavior change in a called helper leaves the function
  body bytes unchanged, so the gate stays silent) — is that objection decisive?
- A proposed third option: **golden-fixture CI tests** for each custom transform
  (recorded input event(s) → expected output event(s)), so a behavior change fails
  at CI rather than at replay — catching the forget-case without coupling replay to
  source and without false-positiving on cosmetic edits.
Recommend among: declaration-only hash (current), declaration + body hash, or
declaration-only hash + mandatory golden-fixture tests.

## Point 3 — SCOPE: single-developer-now vs multi-developer-future

Critical clarification the first prompt did not make: **Endless today is
single-developer, self-hosted** (one human, possibly across multiple git worktrees
on one machine; events already carry per-node IDs from a hybrid logical clock).
**Multi-developer, git-merged, offline collaboration is a POSSIBLE PRODUCT FUTURE,
not a current reality.**

Re-evaluate the whole design against this:
- Which parts are needed NOW under single-developer scope? (The rename→rebuild-
  revert landmine is real even with one developer; the upcasting pipeline, rebuild-
  as-migration, and the ephemeral split all seem needed regardless.)
- Which parts are justified ONLY by the multi-developer future? (The merge-safe
  named+hash identity surviving *concurrent* authorship, cross-branch conflict
  semantics, the atomic-grouping merge behavior of Point 1.)
- Flag anything that is YAGNI under single-developer scope.
- The team's working hypothesis: choose the named+hash identity NOW (it costs
  little over integers and avoids a painful migration later), but build only what
  single-dev needs today. Validate or challenge that "design for both, build for
  one" stance.

## Deliverable
A revised **Part II** (implementation brief) and **Part III** (decision records)
incorporating the four context items, your recommendation on each of the three
points, and an explicit "needed now vs. deferred-until-multi-dev" partition of the
design driven by the scope answer.
```
