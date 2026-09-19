# Analysis

One root cause with four faces: **code and database versions diverge across the
worktree / main / real-ledger boundary, and nothing guards the seam.** All four
were hit for real on 2026-08-10, in one sitting, landing E-1898.

## Face 1 — the land uses a binary built from stale source

`just land` (justfile ~167-250) does, in order:

1. `just go` in the worktree — rebuild endless-go
2. `endless db apply-change` against the REAL DB, using that binary
3. `endless worktree land` — which rebases onto main and fast-forwards

Step 1 came from E-1709, whose comment names this failure: "A worktree rebased
onto a newer main ... but not rebuilt would land with a STALE binary whose
embedded enums no longer match the real DB's rows ... (the E-1664 guard only
checks the binary is PRESENT, not CURRENT)."

E-1709 solved half. It makes the binary match the worktree's SOURCE. Nothing
makes the source match MAIN. With the branch 147 commits behind, `just go`
faithfully rebuilt a still-stale binary and step 2 hit the same wall:

    Error: apply-change failed: open db: gate_kinds integrity check on
      ~/.config/endless/endless.db: gatekind: gate_kinds row id="2" slug="relay"
      has no matching enum constant

Nothing was wrong with gate_kinds. Main's E-1901 had added `GateKindRelay` and
applied the row; the worktree's source predated it. The rebase that would fix
this is step 3 — after the step that needs it.

## Face 2 — apply-change succeeds, the merge fails, the DB is left ahead

After rebasing by hand, the land was re-run. This time step 2 SUCCEEDED and the
land then failed, so main never got the code. Result: the real DB was migrated
to a schema only the unlanded branch understands, while every installed binary
still expected the old one. Every session query referenced a dropped column, so
all 64 sessions went unresolvable and session tracking froze machine-wide (0
writes in the following 7 minutes).

The justfile comment asserts: "If any apply fails, the land aborts before main
advances (clean recovery)." It reasons only about apply FAILING. Apply
succeeding and the merge then failing is the unhandled — and worse — case,
because it is the one that is irreversible.

## Face 3 — no supported way back (SPLIT OUT to E-1942)

`endless db backup` exists and the land calls it. There is no `endless db
restore`. Recovery was an unguided `cp`, and it went wrong twice:

- Copied over a live database with six open connections, leaving a hot
  `endless.db-journal`. A read-only connection cannot roll back a hot journal,
  so every reader returned `database is locked` and the status bar flickered.
- The backup is produced by `VACUUM INTO`, which writes a **rollback-journal**
  database. The live DB was WAL. Restoring silently changed journal mode, and
  every connection then fought over an exclusive lock to switch it back.

Recovery took: `pkill -f 'endless task show'` (four abandoned pagers holding
connections for up to 22 days), killing two `session-status --monitor`
processes, then `sqlite3 <db> 'PRAGMA journal_mode=WAL; PRAGMA integrity_check;'`
to roll back the journal and restore WAL. None of that is discoverable.

A backup you cannot safely restore is not insurance.

## Face 4 — MOVED to E-1944

The worktree's Claude hook runs candidate code against the real ledger (E-998
points the hook at the candidate binary; E-1450 forces hooks to the real DB).
That pair is safe only while the schema is unchanged, and deciding it means
deciding who may define, migrate and use the schema — which is E-1944. It is
out of scope HERE so this task stays spawnable.

## Decided approach

**Face 1 — refuse, do not auto-rebase.** Before rebuilding the worktree binary,
refuse the land when `git rev-list --count HEAD..main` != 0, telling the user to
rebase. Auto-rebasing mid-land is rejected: E-1898's own rebase stopped on a
conflict in the append-only db-ledger JSONL, and a rebase that stalls with main
partly advanced is worse than a clean refusal.

**Face 2 — rehearse the merge, then reorder.** Do the merge on a throwaway
branch first (`endless-land-<taskid>-<rand>`, removed in a trap, so concurrent
lands cannot collide):

  1. branch off main, merge/rebase the task branch onto it
  2. it fails -> delete the branch, abort. The DB was never touched.
  3. it succeeds -> FAST-FORWARD main to that result. A pointer move, so there
     is no window where main advances and something else does not. Do NOT redo
     the merge; re-merging reopens the gap it just closed.
  4. THEN apply the schema changes, then record the landing, then rebuild.

Applying AFTER the fast-forward inverts the asymmetry that caused the incident:

  - apply fails after the merge -> main has the code, the DB lacks the
    migration. Re-run `endless db apply-change`; it is idempotent and gated by
    `_schema_version`. Recoverable.
  - merge fails after apply -> what happened on 2026-08-10. Not recoverable
    without a restore.

**Diagnosis, independent of the above.** Every `VerifyIntegrity` call site can
name the likely cause when the DB has rows the binary lacks: "this binary
predates the database; rebase onto main and rebuild." That direction of drift is
always staleness, never corruption. The reverse (binary has constants the DB
lacks) is a genuinely missing migration and should keep reading as it does now.

## Out of scope

E-1898 itself, which is fixed and re-verified. `endless task show -p` pinning a
DB connection for the life of its pager is a real but separate nuisance; file it
on its own if it bites again.


