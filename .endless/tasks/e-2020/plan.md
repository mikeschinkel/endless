# Plan

E-2019 changed how the schema is defined. This changes when it may be applied,
and it is the land that starts breaking older binaries on purpose. Sequencing
matters more here than anywhere else in the epic.

## Increment 1 — the version comparison

`monitor.DB()` stops bringing databases up to date on connect. It reads the
database's goose version and compares it against the binary's embedded set, then
acts BY DIRECTION:

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

## Increment 2 — refusal is per-surface

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

## Increment 3 — `endless db upgrade`

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

## Increment 4 — remove what this makes unnecessary

- **E-1818's schema-passive mode goes.** It exists to stop a binary mutating a
  database it merely opened; once no connect applies schema there is nothing to
  suppress. That removes `pinnedToForeignRealDB`, `foreignRealDB` and the
  branch around the integrity gates in `monitor.DB()`. The enum integrity checks
  themselves STAY — they answer a different question (does this database's seed
  data match my enums) and a version match does not imply a seed match.
- **`_incomplete_schema_hint` stops naming change files.** Its current message
  branches on "an outstanding change file names the object", and E-2019 deleted
  that directory. It points at `endless db upgrade` and the version mismatch
  instead. (E-2020's description also asks to fix `_is_missing_schema_error`
  treating a missing column as an unschema'd database — E-2036 already fixed
  that; the function it names no longer exists.)

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

- **E-2088 must land first.** It is recorded as blocking this task. Without the
  migration-only executable, a self_dev land has no binary permitted to apply
  its own migration: `_resolve_land_endless_go` hands land the worktree's build,
  which this task teaches to refuse.
- **E-1972 blocks this task.** 89 worktrees are pinned to their own binary and
  every one of them is older than main's; the moment this lands, each halts on
  connect. The hook's silent-no-op-plus-fault keeps that from being noisy, but
  it means those sessions stop being tracked until their binary is rebuilt,
  which is E-1972's whole subject.

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
   versions — the same condition rendering two ways is the point of Increment 2.
6. `endless db upgrade` takes a backup before applying, and the backup restores
   to the pre-upgrade schema.
7. `endless db upgrade` refuses from a candidate binary.
8. Schema-passive is gone: `pinnedToForeignRealDB` and `foreignRealDB` no longer
   exist, and the enum integrity gates still run on every owned connect.
9. No message anywhere names a change file or `endless db apply-change`.
