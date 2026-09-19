# Analysis

Provoked by the 2026-08-10 incident landing E-1898, which produced four distinct
failures in one sitting from one underlying question: **who is allowed to define,
migrate, and use the schema, and when.**

## What governs schema writes today

`monitor.DB()` applies `schema.SQL` — all additive `CREATE ... IF NOT EXISTS` —
on EVERY connection, plus five enum `VerifyIntegrity` gates that fail closed.
Destructive one-off change files run separately, once, at land time via
`endless db apply-change`.

E-1818 added **schema-passive**: when `dbPathOverride != ""` (set by
`PinMainDB`/`ForceRealDB`, i.e. the hook, tmux and channel surfaces) the connect
skips both the schema apply and every gate.

**The gap.** Schema-passive protects the DB from the binary. It does nothing to
protect the binary from the DB. The hook error on 2026-08-10 proves it: the
candidate binary did not migrate anything, it simply failed writing `process_id`
into a table that lacked the column. E-1818 solved one half; the unsolved half
is the one that caused the outage.

## A. What each executable does on connect

| Executable | Surface | Pins real DB? | Applies schema.sql | Runs enum gates |
|---|---|---|---|---|
| global (main) | hook / tmux / channel | yes | no (passive) | no |
| global (main) | task, sql, event, ... | no | yes | yes |
| worktree (candidate) | hook / tmux / channel | yes | no (passive) | no |
| worktree (candidate) | event apply-change | no | YES | YES  <- the gate_kinds abort |
| worktree (candidate) | anything with --config-dir | no | yes (that DB) | yes |
| Python CLI | all | n/a | own bootstrap | none |

## B. Binary schema vs. DB schema

| Binary expects | DB has | Connect | Runtime DML | Observed |
|---|---|---|---|---|
| main | main | ok | ok | normal |
| candidate | candidate | ok | ok | sandbox only |
| candidate | main | ok (passive) | FAILS: writes absent columns | hook errors, 30 min |
| main | candidate | ok (passive) | FAILS: reads dropped columns | blank board, 30 min |

Rows 3 and 4 are one defect seen from either side. NEITHER is detected at
connect; both surface as runtime SQL errors on hot paths. The enum gates cover
enum tables only and would never catch a dropped column.

## C. Landing phases

| Phase | Binary | DB | Schema | What failed |
|---|---|---|---|---|
| 1 rebuild worktree bin | worktree | - | - | built from stale source (147 behind) |
| 2 backup | Python | real | pre | - |
| 3 apply-change | worktree | real | pre -> post | gate abort; later succeeded |
| 4 merge | git | - | - | FAILED -> DB post, code pre |
| 5 record landing | worktree | real | post | never reached |
| 6 just build | - | - | - | never reached; global stayed pre |

## Hypotheses to evaluate

**H1 — a migration-only executable.** Built from the landing branch, does DDL
only, never DML, so it has no runtime schema expectations to be wrong about.
Cleanly resolves phase 3. Does NOT touch table B rows 3/4, which are ordinary
binaries at runtime.

**H2 — explicit upgrade; connects verify instead of migrate.** Replace
apply-on-connect with an `endless db upgrade` verb; ordinary connects compare
the DB's schema version against the binary's and REFUSE with a clear message.
This is the one that collapses table B: rows 3 and 4 become a precise
connect-time refusal, and no binary can mutate the real ledger by opening it —
which also makes schema-passive unnecessary. Costs users an explicit step after
upgrade, and every entry point needs a good "your database needs upgrading"
message.

**Neither resolves everything, and that should be stated up front:**
- Ordering (phase 3 vs 4) is independent — see E-1941. H2 arguably makes a
  half-landed state MORE visibly broken (everything refuses) rather than less.
- Recovery is independent — see E-1942, whose shape depends on this decision:
  "copy a backup back" vs "upgrade --rollback" are different verbs.
- Whether candidate hook code should write to the real ledger at all (the
  E-998 / E-1450 tension) is a policy question this may inform but does not
  answer.

## The tension, named

E-998 points a self-dev worktree's hook at CANDIDATE code so it gets exercised.
E-1450 forces hooks to write to the REAL ledger so activity is real. Each is
correct alone; together they mean unlanded code writes production data, which
is safe exactly while the schema is unchanged and unsafe the moment it is not.

It is not a genuine catch-22. It is unresolvable only while one binary defines,
migrates, and uses the schema at once. Separating define/migrate from use — H1
and H2 together — collapses it.

## Deliverable

A decision plus the follow-on tasks it implies. Not an implementation.



## Folded in from E-1922: the concrete form of H2

E-1922 was filed separately as an implementation of exactly H2's connect-time
check, and is folded here because it presupposes a decision this task has not
yet made. Its evidence stands regardless of which hypothesis wins, and is worth
keeping in front of whoever decides:

`monitor.DB()` executes its embedded schema SQL on every owned connection with
no check that the DB carries the change files that schema assumes. Nothing
connects a binary's expectations to the DB's actual state, so a binary
installed before its change file is applied can create schema objects
referencing columns that do not exist. E-1917's trigger reads
`NEW.changed_by_session`, and SQLite resolves trigger bodies at fire time, so
every `UPDATE tasks` would fail — the failure is not at connect, it is at the
next write, which is what makes it hard to attribute.

Today that is avoided only by convention: land, which runs apply-change, before
`just install`. A convention is not a guard, and the ordering requirement is
invisible to anyone who has not been bitten by it.

E-1922 proposed a gate that either auto-applies pending changes on connect or
refuses with a clear error. Note that those are two different answers, not one:
auto-applying keeps a binary able to migrate whatever DB it opens, which is the
property H1 and H2 are both trying to remove; refusing is H2 proper. If H2 is
chosen, this becomes its first follow-on task. If H1 is chosen instead, the
convention-only ordering requirement still needs an answer, and this evidence
is where that answer starts.
