Implements ED-1571, the one part of E-1944's outcome that was decided and never
filed.

Under ED-1567 a CANDIDATE binary may never migrate the real ledger. But
`_resolve_land_endless_go` in src/endless/worktree_cmd.py hands a self_dev land
the WORKTREE's own binary, and E-1664 made that an invariant rather than a
choice: "the only binary whose embedded schema/enums match the just-written rows
is the worktree's own build — never the not-yet-refreshed global." That binary is
a candidate by `candidateBuild()`'s definition — its path carries the worktree
marker.

So the moment E-2020 lands, a self_dev land has no binary permitted to apply its
own migration: the installed one does not carry it, the worktree one is not
allowed to run it. That is why this exists, and it is why it blocks E-2020 rather
than following it.

Build a migration-only executable from the landing branch, carrying the
migration set and nothing else. It performs migration steps in FULL — DDL plus
the DML those migrations define: enum seed rows, backfills, the INSERT ... SELECT
of a table rebuild. What it excludes is application runtime: it never serves a
hook, never runs a task command, never touches business data outside a
migration. That exclusion is what makes it immune to the
binary-expects-a-schema failure, because it has no expectations beyond the
migrations it carries.

`worktree land` builds and invokes it at the existing apply step (E-1941's
ordering: after the ff-merge, before `_record_landing`) instead of calling
`endless db apply-change` with the worktree binary.

Scope is self_dev ONLY. Every other project has one installed binary and no land
at all, so it carries and applies its own migrations under ED-1570; no separate
executable exists there, and nothing in this task may assume one does.

Two things to settle while planning, not now:
- `_record_landing` still runs on the worktree binary for the same E-1664
  reason, against a ledger this executable just migrated. Whether that remains
  correct under ED-1570's exact-agreement rule needs an answer.
- E-1709 rebuilds the worktree binary before land uses it. If the migration
  executable replaces that usage, E-1709's rebuild may be serving only
  `_record_landing` afterward.

# Verification

`.endless/tasks/e-2088/`, run only through the runner — `just verify E-2088` or
`endless task verify E-2088`. Never executed directly: `_guard.sh` refuses that,
and a suite exercising land-time migration against your real `HOME` is precisely
the run that must not happen.

Shape: a `verify.toml` whose first `[[check]]` is a `gotest` runner over the
executable's unit tests — the fail-fast half — plus a `verify.sh` driving a land
end-to-end. The script sources `_harness.sh`, opens with the DO-NOT-EDIT header
naming E-2088, uses `section` / `assert_eq` / `assert_contains`, and calls
`summary` last; shipped fixtures are read from `$ENDLESS_VERIFY_DIR`.

The runner's temp `HOME` and `XDG_CONFIG_HOME` are what make it safe to run a
real migration, so nothing here passes `--config-dir`.

1. The executable applies a migration end to end against a throwaway database:
   version before, version after, and the DML the migration defines actually ran
   (seed rows present, backfill applied), not just the DDL.
2. It refuses to do anything that is not a migration — assert the binary exposes
   no hook, task, or query surface, since "it never serves a hook" is the
   property that makes it immune to the binary-expects-a-schema failure and is
   otherwise untested.
3. `worktree land` invokes it at E-1941's apply step, between the ff-merge and
   `_record_landing`, and a failure there produces the existing post-merge
   message telling the operator to re-run rather than restore.
4. Landing a branch with no migration does not build or invoke it.
5. In a non-self_dev project no such executable exists and the installed binary
   applies its own migrations under ED-1570 — assert the self_dev-only scope
   rather than assuming it.
6. The worktree binary is still what `_record_landing` runs, against the ledger
   this executable just migrated. Assert that pairing works, because it is the
   half of E-1664 that survives E-2088 and nothing else covers it.

Coverage that must outlive the land is mirrored into the durable Go suite —
assertion 2's "no application surface" check in particular, since it is a
property of the build that would otherwise erode silently.

# What this grew to include

Three things larger than the task row described, folded in because each was
cheaper to do than to file and none of them widens the land's blast radius.

## internal/dbcontext — extracted, not duplicated

The executable needed to know where the database lives, and that answer lived
inside `internal/monitor`. Importing monitor for one path join would have put
the entire application — the schema-applying `monitor.DB()` above all — into a
binary whose only claim to safety is that it carries nothing but migrations, and
would have made the "no application surface" assertion a statement about the
command line rather than about the build.

So the resolution moved into a leaf package (`ConfigDir`, `DBPath`,
`ConsumeConfigDirFlag`) and `internal/monitor` delegates to it, keeping its own
routing — the hook's main pin, cwd sandbox self-detection, the explicit-context
gate — layered on top. One definition, two callers, and the executable's
dependency graph became an assertable property: a durable Go test reads
`go list -deps ./cmd/endless-migrate` against an ALLOWLIST of exactly
`dbcontext` + `schemachange` + the cmd itself, so any future import of an
application package fails a test rather than quietly undoing ED-1571.

The executable deliberately does NOT self-detect a worktree sandbox from cwd.
That is right for an application surface and wrong for a migration: a tool that
rewrites a schema resolves its target from what the caller named, never from
where it happens to be standing.

## internal/schemachange — one definition of applying a change

Rather than a second copy of the apply-and-record logic inside the new
executable, `runApplyChange` moved out of `internal/eventcmd` into
`internal/schemachange`, which takes an open handle and never opens a database
itself. The two applying programs now differ ONLY in how they opened one:
`endless-go event apply-change` through the application's connect, the migration
executable through a direct file open. That difference IS ED-1571, so it is the
only difference left in the code.

`internal/schema/changes/runner` (the helper a `.go` change imports) was pointed
at the same two packages, so a compiled change script links the migration
machinery and no application either. Same answer, same reason.

## E-1941's durable land tests were updated, not bypassed

`tests/test_worktree_land_schema_apply.py` asserted the land's call order as
rebuild → backup → apply → record and monkeypatched
`event_bridge.apply_change` to do it. The apply is no longer that call, so the
module was updated to the real sequence — rebuild → build-migrate → backup →
apply → record — with both builds asserted to happen while base is still
behind. That module is the single definition of the land's ordering; leaving it
patched around a stale call would have left the ordering unasserted.

# Where the build sits, and why it is not exactly "at the apply step"

The task row says the land "builds and invokes it at the existing apply step".
The INVOKE is exactly there — between the ff-merge and `_record_landing`,
E-1941's window, unchanged. The BUILD is one step earlier, at 4.6: after the
change list is known (so a land with no migration builds nothing, which is what
makes its absence testable) and before the ff-merge, for the reason E-1941 gave
for the endless-go rebuild it sits beside — "a broken build aborts while main
and the DB are untouched, strictly safer". A tree that cannot compile its own
migration tool should not get as far as advancing base.

# The two questions the plan left open, answered

`_record_landing` KEEPS running the worktree's endless-go, and the split is the
point. Applying is forbidden to an unlanded build; recording is a DATA write,
which an unlanded build has always been allowed to make against the real
database, and it is the write whose enum constants must agree with the rows the
migration just inserted. The verification suite asserts that pairing end to end:
the executable migrates a ledger-shaped database, then the application's
connect — schema.sql plus all four fail-closed integrity gates — opens it and
honours the marker the other program wrote.

E-1709's pre-land rebuild therefore still has a consumer. After ED-1571 it
serves `_record_landing` and the pre-apply backup rather than the apply, and
both still run that binary, so nothing about it changes.
