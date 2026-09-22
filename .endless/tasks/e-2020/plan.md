# Plan

E-2019 changed how the schema is defined. This changes when it may be applied,
and it is the land that starts breaking older binaries on purpose. Sequencing
matters more here than anywhere else in the epic.

## Increment 1 — the version comparison

`monitor.DB()` stops bringing databases up to date on connect. It reads the
database's goose version and compares it against the binary's embedded set, then
acts BY DIRECTION.

E-2019 already built both halves of that comparison and said so: `schema.DBVersion()`
and `schema.LatestVersion()` exist, and `LatestVersion`'s own comment names the
"your database is ahead of your binary" case as their purpose. This increment
composes them; it does not invent them. `monitor/db.go` also carries an explicit
hand-off — "WHEN it is applied has not changed — still every connect — only
where it comes from; E-2020 is what replaces this with version verification" —
so the block to rewrite is unambiguous.

Fix one wrong sentence in that same block while rewriting it: it claims "a
database built before versioning existed is stamped at the baseline rather than
replayed onto". Nothing stamps anything. `migrate.go` is explicit that the
baseline is written idempotently so the one pre-versioning database "reaches
this version by a replay that changes nothing, rather than by an assertion that
it already had", and there is no stamping code anywhere. The comment states the
opposite of the design, in the function this task rewrites.

- **Database BEHIND an INSTALLED binary** — auto-apply forward. The binary needs
  DDL that is absent and it owns the schema, so applying is correct and
  unconditional. In a non-self_dev project this is the only case that ever
  fires, because there is one binary.
- **Database BEHIND a CANDIDATE binary** — refuse. A candidate may never migrate
  the real ledger; that is the 2026-08-10 failure exactly. `candidateBuild()`
  already answers this, keyed on the executable's path rather than cwd, which is
  the right question and already the tested one.
- **Database AHEAD of ANY binary** — halt. Per ED-1570 a binary works with
  exactly one schema version, so there is nothing to evaluate: the binary must be
  upgraded. Rollback is `endless db restore` from the backup `db upgrade` takes.

There is no compatibility window and no additive-migration exemption. ED-1570
settled that an older binary writing a newer database is unsafe even when every
intervening migration was additive, because the new columns exist precisely
because new code populates them, so the rows an old binary writes are
permanently incomplete and no later upgrade recovers what was never recorded.

## Increment 2 — seeding survives, and must be split out first

`schema.Migrate()` does TWO things: `provider.Up()` and then `Seed()`. Removing
the `Migrate()` call from the connect path therefore removes the enum-mirror
reseed as a side effect, and that is a regression, not a simplification.

`seeds.sql` states the cadence it needs: the mirrors are "reconciled on every
connect", because they are DATA derived from Go enums rather than schema — "a
migration runs once, and a mirror that runs once stops being a mirror the moment
the Go enum it mirrors is edited". It also names what depends on the ordering:
"E-1659's self-heal survives the move to goose: a row seeded under an old slug
or label is reconciled by the upsert on the next connect, BEFORE monitor.DB()'s
VerifyIntegrity gates read it."

So the connect path keeps calling `schema.Seed()` unconditionally, on both the
applied and the refused branches, before the integrity gates run. Drop it and a
drifted mirror row stops self-healing and instead fail-closes the binary at the
gate — turning a version check into an outage on exactly the databases it was
meant to protect.

This is the one place the plan's original "the enum integrity checks themselves
STAY" was necessary but not sufficient: keeping the CHECKS while removing the
RESEED that feeds them is worse than keeping neither.

## Increment 3 — refusal is per-surface

A single failure mode rendered one way is wrong here, because the hook fires on
every event: erroring loudly turns one mismatch into machine-wide noise, which
is the 2026-08-10 experience of reading fifty identical lines and not knowing
which mattered.

- **The Claude hook**: silent no-op plus a recorded fault. This follows E-1962
  exactly, whose reasoning transfers unchanged — "A hook that logged or exited
  non-zero would surface as a Claude Code hook failure on every event, turning
  'we don't support this' into a stream of errors for the user to chase."
  Substitute "we cannot read this database" and the sentence still holds. The
  fault is recorded once and deduplicated by fingerprint, so the evidence
  survives without the noise; a new code goes in `internal/faults/codes.go`.
  Returns nil, writes nothing to stdout — a hook's stdout is one JSON document
  and no output is how a hook says "no action".
- **Interactive CLI surfaces**: refuse loudly, naming the two versions and the
  command that fixes it. A person at a terminal asked a question and is owed an
  answer, including "I can't".
- **Background jobs and the tmux status line**: silent, with the fault. Same
  reasoning as the hook — high frequency, no reader.

## Increment 4 — `endless db upgrade`

The explicit path, and the only one in a self_dev checkout.

- Python command shelling to Go, matching `db apply-change`, `db backup` and
  `db restore`. It is an always-main operation and pins accordingly.
- **Takes a backup first.** This is load-bearing rather than polite: since the
  direction rules refuse to let an older binary write a newer database, restore
  IS the rollback mechanism for a bad release. `endless db restore` already
  exists.
- Refuses when run from a candidate binary, for the same reason connect does.
  In self_dev the sanctioned path at land is E-2088's migration executable; this
  command is for the installed binary catching up after one.

## Increment 5 — remove what this makes unnecessary

- **E-1818's schema-passive mode goes.** It exists to stop a binary mutating a
  database it merely opened; once no connect applies schema there is nothing to
  suppress. That removes `pinnedToForeignRealDB`, `foreignRealDB` and the
  branch around the integrity gates in `monitor.DB()`. The enum integrity checks
  themselves STAY — they answer a different question (does this database's seed
  data match my enums) and a version match does not imply a seed match.
- **`_incomplete_schema_hint` — DEFERRED to E-2158, not done here.** An earlier
  draft of this plan had it stop naming change files "a directory E-2019
  deletes". E-2019 did not delete it: `internal/schema/changes/` survives,
  `endless db apply-change` survives, and land still applies change files —
  now through E-2088's `endless-migrate apply` rather than the old runner. New
  change files are still being written (`e-1000-rename-tasks-text-to-plan.go`).
  The retirement was cut out of E-2019 into **E-2158**, "Delete the change-file
  mechanism and move land-time apply onto goose", which is `submitted`.

  So the message is still TRUE today, and rewriting it here would edit a string
  E-2158 is about to delete outright. Leave it. What this task must not do is
  ship a version-mismatch diagnostic that sends the user to `apply-change`; the
  new message this task adds points at `endless db upgrade`, and the old
  change-file branch stays until E-2158 removes its subject.

  (E-2020's description also asked to fix `_is_missing_schema_error` treating a
  missing column as an unschema'd database — E-2036 already fixed that and the
  function no longer exists. The description was corrected; this note records
  why there is nothing to do.)

## Python gets no version check, and the reason is not expedience

Python has no embedded migration set, so it has no version of its own to
compare. The version is a property of the GO binary's migrations. A Python read
against a database whose shape it does not expect fails on the column it named
and is rendered by `_incomplete_schema_hint`, which is the existing, correct
behaviour. Adding a check to Python would mean inventing a Python-side notion of
"the version this code expects", which is a second source of truth for exactly
the fact this epic is consolidating.

# Sequencing

This task is where stale binaries begin to halt, so it cannot land alone.

- **E-2088 has LANDED** (2026-09-16). `cmd/endless-migrate` exists, and
  `worktree land` already resolves and invokes it via `_resolve_land_migrate_bin`
  at the apply step. The precondition this task had is satisfied.
- **E-2019 has LANDED** (2026-09-17), and deliberately left this task its hooks:
  goose drives the connect path, `DBVersion`/`LatestVersion` exist, and the
  block to replace is commented as belonging to E-2020.
- **E-2158 touches the same file and should not run concurrently with this.**
  It deletes the change-file mechanism; this task leaves `_incomplete_schema_hint`
  alone precisely so the two do not collide. That is E-2164's `<>` case —
  two tasks whose worktrees touch the same paths — and no relation expresses it
  today; `precedes`/`preceded_by` and the `<>` marker both arrive with E-2164.
  Until then the constraint lives here, in prose, deliberately rather than as a
  `relates_to` standing in for a relation that does not exist yet.

  If E-2158 lands first, re-read the `_incomplete_schema_hint` bullet above:
  the deferral becomes moot because its subject is already gone.
- **E-1972 blocks this task, and NOT because stale binaries start halting.**
  An earlier draft said they would. They do not: the version check lives in the
  binary doing the connect, so a build that predates this task carries no check
  and cannot halt. Measured 2026-09-20 — 134 of 142 worktrees carry their own
  binary and 110 of those predate E-2019's land, so they hold no goose set at
  all. Landing this task leaves every one of them doing exactly what it does
  today: `pinnedToForeignRealDB()` sends it down the schema-passive path, which
  skips `Migrate()` and all four `VerifyIntegrity` gates, and it writes data
  against a schema that has moved under it. That is ED-1570's
  permanently-incomplete case, and it is untouched by this task.

  So what this task achieves ALONE is narrower than it looks: the guard reaches
  a session only once that worktree has rebuilt, and the worktrees most likely
  to be dangerous are the ones least likely to have rebuilt. E-1972 is what
  routes hooks through main's known-good binary, which is what puts the check in
  front of every session rather than only freshly-built ones. Without it this is
  a guard the binaries needing guarding do not execute.

  The cost profile inverts the same way. Once Increment 5 removes schema-passive,
  the halting population is not a one-time backlog to drain — it is every
  worktree whose branch has not rebased past the newest migration, recurring
  every time anyone lands one. Across 142 worktrees that is steady state, not a
  migration cost paid once, and E-1972 is what keeps it from being a permanent
  tax.

# Verification

`.endless/tasks/e-2020/`, run only through the runner — `just verify E-2020`
(which routes `--db sandbox`, so the gate exercises the CANDIDATE binary rather
than main's) or `endless task verify E-2020`. Never executed directly:
`_guard.sh` refuses that, and this suite drives real hooks, which is the case
the refusal exists for.

Shape: a `verify.toml` whose first `[[check]]` is a `gotest` runner over the
direction-decision unit tests — the fail-fast half — plus a `verify.sh` for the
end-to-end hook assertions. The script sources `_harness.sh`, opens with the
DO-NOT-EDIT header naming E-2020, uses `section` / `assert_eq` /
`assert_contains`, and calls `summary` last; shipped fixtures are read from
`$ENDLESS_VERIFY_DIR`.

Isolation comes from the runner's temp `HOME` and `XDG_CONFIG_HOME`, not from a
flag. The hook still calls `PinMainDB` — that is the behaviour under test — but
"main" resolves inside the temp home, so it lands on a throwaway database. Do
not reach for `--config-dir` to arrange this; the runner already has.

Coverage that must outlive the land is mirrored into the durable Go suite:
assertion 1 is the pure decision function and belongs in `internal/monitor`'s
own tests, where it will keep being run.

1. Each direction, as a unit over the pure decision function: behind+installed
   applies, behind+candidate refuses, ahead halts for both.
2. No connect applies schema: point a binary at a database one version behind
   and, on the candidate path, assert `sqlite_master` is byte-identical before
   and after.
3. The hook is silent on mismatch: exit 0, empty stdout, no stderr, and exactly
   one fault recorded. Assert the fault, because "silent" and "did nothing" are
   the same observation otherwise.
4. The fault deduplicates: fifty hook events on a mismatched database produce
   one incident with occurrence 50, not fifty incidents.
5. An interactive CLI surface refuses loudly on the same database, naming both
   versions — the same condition rendering two ways is the point of Increment 3.
6. `endless db upgrade` takes a backup before applying, and the backup restores
   to the pre-upgrade schema.
7. `endless db upgrade` refuses from a candidate binary.
8. Schema-passive is gone: `pinnedToForeignRealDB` and `foreignRealDB` no longer
   exist, and the enum integrity gates still run on every owned connect.
9. No message anywhere names a change file or `endless db apply-change`.




