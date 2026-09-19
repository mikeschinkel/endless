# Epic: event-upcasting pipeline for rebuild-db

Implements the design resolved in **E-1652**; full implementation brief is the
**E-1666** outcome (revised Part II/III). This plan is a **candidate workstream
breakdown for Mike to scope/split** — not final. Status intentionally left
unapproved pending his review.

## Objective
Let `rebuild-db` replay historical events into the current schema without ever
rewriting the committed ledger, correct under both concurrency regimes (single-dev
= the empty-conflict case of multi-dev), with conflicts surfaced for human
resolution (auto-resolution deferred).

## Candidate workstreams (likely child tasks)
1. **Go upcasting package** (std-lib only; no SQLite/CLI/ledger-IO): forward-only
   declarative ops (`rename`, `add`, `remove`, `hoist`, `plunge`, `wrap`, `head`,
   `in`, `map`, `convert`) + host-registered `custom` escape hatch; linear per-kind
   chain walk; name+content-hash identity; **manifest-of-references** migrations;
   canonical-form hashing; integrity gates (hash mismatch / unregistered custom /
   missing referenced entry = hard error).
2. **Two-ledger scope split:** declare `scope` (project | machine-user) per event
   kind, required (hard error if undeclared); emit-time routing gate
   (project→`.endless/db-ledger/`, machine-user→`.endless/db-ledger/local/`,
   gitignored); reclassify `session_status.*`/`focus.*` as machine-user; eliminate
   today's muddy bucket.
3. **rebuild-db integration:** build the structural destination first (`schema.sql`
   + `internal/schema/changes/`), then replay project events through the package;
   swap only project-scope tables; leave live machine-user tables untouched.
4. **Diagnosability:** coverage linter (land-time; decidable via `covers`) +
   cheap per-prompt tripwire in the hook (applied-set ⊆ registry, no machine-user
   kind in committed ledger, row-count sanity, registry integrity); semantic-
   collision + structural-pairing checks.
5. **Rebuild trigger:** detect a migration-set addition on land/pull; rebuild in
   the existing land/rebase step (no git hook). Changed-hash on an existing
   migration = hard error (history was edited).
6. **Golden-fixture test harness** for custom transforms (CI gate, per ADR-001).
7. **Consolidate the existing E-1378 decision shims** (mapLegacyDecisionStatus,
   replayLegacyDecisionCreated, type=="decision" routing, projectorTypeID NULLing,
   dropped-`prompt` skip) into the pipeline as the seed migrations.
8. **First consumer / E2E validation:** E-1659 (task type `task`→`todo`,
   `bug`→`bugfix`) — the rename that proves replay-through-transforms works.

## Known defect this epic must close (folded from E-1907, 2026-08-06)

`replayEvent`'s `default:` branch — commented "Skip non-task events silently
(sessions, notes, etc.)" — drops every kind it has no case arm for and returns
nil. **23 of the 43 kinds in `ValidKinds` hit it.** Nothing distinguishes a
deliberately-skipped kind from a typo, a kind emitted by a newer binary, or one
removed from the vocabulary: all four are silently identical at replay.

`ValidKinds` does not help. It is consulted only by `Event.Validate()`, which
runs only on the four emit paths in `internal/eventcmd/event.go`. The read path
(`ReadAllEvents` -> `ProjectToTempDB` -> `replayEvent`) never validates. So
removing a kind from the closed set cannot fail a rebuild today — but that is
because replay checks nothing, not because retirement is handled.

Two separable cases, which land in different workstreams:

- **Absent from `ValidKinds` entirely** (typo / removed kind / newer binary).
  Decidable against the closed set alone, with no classification needed. This is
  workstream **4**'s tripwire (`applied-set ⊆ registry`) and should also warn
  into `ProjectResult.Errors`, which `rebuild-db` and `validate-db` already
  print.
- **In `ValidKinds` but with no replay arm** — the 23 below. NOT independently
  decidable: they are dominated by `session.*`, `conversation.*`, `message.*`
  and `session_status.*`, exactly the kinds workstream **2** reclassifies as
  machine-user scope and routes to a separate ledger. Classifying them before
  that split would pre-empt it.

The 23, as of 2026-08-06:

    task.released            session.chat_started     conversation.closed
    task.claimed             session.idled            message.sent
    project.registered       session.ended            message.delivered
    project.updated          session.task_completed   note.created
    project.renamed          session.hidden           note.resolved
    project.unregistered     conversation.beaconed    session_status.recorded
    project.purged           conversation.connected   session_tasks.ordered
    session.work_started                              project_next.revised

Workstream **7** already folds "the dropped-`prompt` skip" into the pipeline as
a seed migration; a retired *kind* is the same class of thing and belongs in the
same consolidation, so retirement should be a pipeline concern rather than a
separate registry.

**Provenance.** Surfaced during E-1906, which removed the `session.recapped`
kind and its payload. That removal is safe under today's behavior: no emitter
ever existed (the kind was introduced by `f99b9ab0` / E-804 as vocabulary and
never wired up), and no ledger line anywhere references it — a premise pinned as
check 7 of `tests/tasks/e-1906-verify.sh`. The hazard above predates E-1906 and
is independent of it. E-1907 was filed for it, then obsoleted into this epic
because workstreams 2, 4 and 7 already own the mechanism.

## Deferred (out of this epic unless promoted)
Baseline/compaction to retire old migrations; automatic conflict resolution;
bidirectional lenses; graph shortest-path.

## Coordination
Touches the same emit/rebuild paths as the E-1667 self-dev-build epic; sequence so
they don't collide on `internal/events` / `internal/eventcmd`.

