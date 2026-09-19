# Outcome: adopt versioned migrations (goose); schema.sql is demoted to documentation

## The question, restated

Who may define, migrate, and use the DB schema, and when. Today the answer is
"every binary, on every connection, implicitly" — `monitor.DB()` applies
`schema.sql` on connect. That is the root of the 2026-08-10 outage and of every
variant found since.

## What was decided

1. **`pressly/goose` becomes the canonical source of schema.** Not a
   roll-our-own versioning scheme. Chosen over golang-migrate (SQL-only; the
   five existing `.go` change files that query mid-migration have nowhere to go)
   and Atlas (declarative, external CLI + HCL; heavier than this needs).
   goose fits because it supports SQLite, supports **Go** migrations as well as
   SQL, embeds into the binary via `//go:embed` + `SetBaseFS`, and is usable as
   a library rather than only a CLI.

2. **`schema.sql` stops being the source of truth.** Fresh databases are built
   by replaying migrations. `schema.sql` survives only if it can be *generated*
   from the migrations, as documentation — a readable snapshot of the full
   schema for a human studying it. Generated or not, it is never applied.

3. **No more apply-on-connect.** Schema change happens through an explicit
   `endless db upgrade`, and a connect compares versions and acts **by
   direction**:
   - *DB behind the binary* — the binary needs DDL that is absent. An
     **installed** binary auto-applies forward (unconditional in non-`self_dev`,
     which has one binary). A **candidate** binary refuses; it may never migrate
     the real ledger, which is the 2026-08-10 failure exactly.
   - *DB ahead of the binary* — **halts**, for any binary. There is no
     compatibility window.

   Accepted consequence: a `self_dev` branch carrying an unlanded migration has
   its candidate hook degraded for the whole pre-land window, reducing what
   E-998 exercises on precisely those branches. That is the price of never
   letting unlanded code mutate the production schema.

4. **Exactly one schema version per binary.** No compatibility window, no set
   of compatible versions to derive or maintain. An older binary writing a newer
   database is unsafe *even when every intervening migration was purely
   additive*: the new columns exist because new code populates them, so rows the
   old binary writes are permanently incomplete, and upgrading forward again
   cannot recover what was never recorded. SQLite nullability makes such a
   column optional to the **store**, never to the application. Rollback after a
   bad release is served by restoring a pre-upgrade backup, not by tolerating a
   mismatch.

5. **In `self_dev`, land-time migration runs from a migration-only executable**
   built from the landing branch, carrying the migration set and nothing else —
   because that is the only mode where a candidate and an installed binary
   coexist against one real ledger, so during the pre-land window nobody else
   may migrate it: the candidate must not, and the installed binary does not
   carry the unlanded migration. **Every other project has one installed binary
   and no land at all**, so it carries and applies its own migrations under (3)
   and (4); no separate executable exists there. It performs migration steps in **full**, DDL *and* the DML those
   migrations define — enum seed rows, backfills, the `INSERT ... SELECT` of a
   table rebuild. (`schema.sql` seeds `session_kinds`, `process_kinds` and
   `task_types` today, so a DDL-only rule would leave a migrated database
   unusable.) What it excludes is application runtime: it never serves a hook,
   never runs a task command, never touches business data outside a migration.
   That is what makes it immune to the binary-expects-a-schema failure.

## History this reverses, on purpose

Endless had version-based migration on day one. It was removed because it
produced a stream of bugs, and `schema.sql` as single source of truth was
adopted to simplify. That trade was correct while the schema changed rapidly.
It is no longer correct: the project is mature, schema changes are infrequent,
and the simplification's cost has moved from "fewer migration bugs" to
"outages when two binaries disagree about the schema." This decision knowingly
re-adopts what was once removed, with a library instead of hand-rolled code —
because versioning is the part that should not be owned in-house.

## Evidence that forced it

- **2026-08-10 (E-1898 land):** candidate binary wrote `process_id` to a real
  ledger lacking the column. Connect clean, runtime failure, session tracking
  down 30 minutes.
- **E-2010:** the same defect on the **read** side — `no such table` on a view.
  This matters because it rules out a cheaper design: a check on write paths
  only is insufficient. The check must be at connect.
- **`_schema_version` is not a version.** It is a set of applied change-file
  basenames. Additive change goes straight into `schema.sql` and leaves no
  trace at all — so a "compare versions" check had nothing to compare on
  exactly the path that caused the outage. This is why a real migration system,
  not a version stamp bolted onto the current design, is the answer.
- **Two parallel definitions, hand-synced.** `internal/schema/changes/e-1929-add-tasks-removed.go:94`
  creates `live_tasks`, and `schema.sql:347` creates it again. A populated DB is
  correct only because an author remembered to write the same DDL twice. The
  comment at `schema.sql:329-346` (CREATE VIEW resolves lazily, CREATE INDEX
  eagerly, so an index in `schema.sql` would deadlock its own rollout) shows how
  subtle getting that right is. Generating `schema.sql` removes the class.

## What this makes unnecessary

E-1818's **schema-passive** mode exists to stop a binary mutating a DB it
merely opened. Once no connect applies schema, nothing needs suppressing — the
mode should be removed rather than carried forward.

## Open

Nothing. The two questions this brainstorm left open — the non-`self_dev`
upgrade policy and how a linear version number could ever express a
compatibility range — were both resolved before any implementation work began,
and are folded into decisions (3) and (5). The task filed to answer them
(E-2022) was obsoleted rather than worked.

**E-1942 (`endless db restore`)** was blocked pending this decision because its
shape depended on it. It now has one, and it is load-bearing: since (4) refuses
to let an old binary write a newer database, **restore is the rollback
mechanism**. `endless db upgrade` takes a backup first, and reverting a bad
release means restoring that file. Unblocked.

## Considered and rejected

**Expand-contract** (add the replacement, keep the old object working, drop it
in a later land) and the **compatibility range** built on it were both proposed
and rejected. Expand-contract exists to create a window in which two binary
versions both work; (3) already refuses instead, so the window was ceremony
aimed at a case that is not the problem — and it only ever applied to
*DB-ahead*, while the painful `self_dev` case is *DB-behind*, which no map can
make compatible. Its safety also depended on the new binary dual-writing both
halves, whose failure mode is semantic divergence that `PRAGMA integrity_check`
reports as healthy. The compatibility range was then rejected on its own merits
per (4). Rejecting both removes the need to classify migrations as additive or
breaking at all, and with it a build-time classifier.

## Not addressed here

Landing **order** (E-1941) is independent. Whether candidate hook code should
write to the real ledger at all (the E-998 / E-1450 tension) is a policy
question this informs but does not answer — though it dissolves the acute form,
since separating define/migrate from use means unlanded code can no longer
mutate the production schema by opening it.
