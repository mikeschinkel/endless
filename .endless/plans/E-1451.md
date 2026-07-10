# Allow endless CLI ledger writes from a non-Claude shell

## Problem

The E-1401 gate refuses ledger writes from any shell that isn't a registered Claude pane. This blocks:

1. **Mike at his main-checkout shell** filing a task or running a one-off `task replace`.
2. **Scripts/automation** that need to record state changes (release tooling, CI helpers, batch scripts).

**Live incident 2026-05-21:** Mike attempted to run `bash /tmp/snapshot-removal-ledger-ops.sh 1` from his main checkout shell to execute a batch of ledger operations for the snapshot-removal work (E-1448 and follow-ons). Gate refused. Mike bypassed by manually inserting a `sessions` row with id=490; this is undocumented, fragile, and not a stable interface.

## Hard prerequisite: session discovery must be rock-solid first

This task must NOT be implemented until session discovery for actual Claude sessions is verified rock-solid in production. Otherwise we risk attributing Claude-session writes to a user/shell session when discovery fails — that's worse than the current refusal, because the audit trail would be wrong rather than absent.

Blocked-by: TouchSession-always-real-DB task (paired filing) and E-1407 (end-to-end session_id coverage tests). Both must confirm a stretch of zero session-misattribution incidents before this task moves to ready.

## Design decisions (Mike, 2026-05-21)

### Storage

Non-Claude sessions get rows in the same `sessions` table as Claude sessions. Distinguishing column: `harness` = `shell` (vs `claude`). No new tables, no users table, no authorization.

### Synthetic session IDs

Two flavors:

1. **Single global synthetic UUID for `--user-session`** = `00000000-0000-0000-0000-000000000000` (locked, Mike 2026-05-21). One row, idempotent UPSERT, lasts forever. All `--user-session` writes attribute to this one row. Forensics show "a non-Claude shell did this" without per-user/per-host granularity. Acceptable per YAGNI no-users-table. The all-zeros form follows the conventional "nil UUID" pattern (RFC 4122 §4.1.7), which reads cleanly as "no specific identified actor" in audit queries.

2. **Auto-generated fresh UUID for `--new-session`.** New row per invocation (or per `endless session start` call — see below). Used by automation that needs per-invocation forensic distinctness.

The all-zeros constant is defined in one place (e.g. a `SyntheticUserSessionID` constant) and reused.

### New verb: `endless session start`

For stable multi-operation shell sessions: `endless session start` creates a fresh `sessions` row (harness=shell, fresh UUID), prints `export ENDLESS_SESSION_ID=<uuid>` for the user to eval, and that env var carries through subsequent `endless` calls.

**Hard gate:** `endless session start` MUST refuse if invoked from inside a Claude session (detect via `CLAUDECODE=1` env or similar). Claude sessions already have their own attribution; this verb only makes sense outside Claude.

Naming: `start` over `begin` for consistency with other verbs.

### New flags on writing verbs

`endless task add/update/replace/...` (and other writing commands) gain two new flags:

- `--user-session` — use the global synthetic UUID. Implies harness=shell.
- `--new-session` — mint a fresh UUID for this single invocation. Implies harness=shell.

**Hard gate (both flags):** MUST refuse if invoked from inside a Claude session. Same detection as `session start`. These flags exist specifically because there is no Claude session to attribute to; using them from inside Claude is a mistake.

### Explicit-only; no TTY-detection magic

The user (or script) explicitly invokes one of the above. The CLI does NOT auto-detect "you're on a TTY with no `ENDLESS_SESSION_ID`, attribute to shell." Mike: "We may allow for tty later, but I first want to live with it to see if it is problematic and to identify use-cases."

### Migration of existing manual-inserts

Existing manually-inserted `sessions` rows (Mike's bypass id=490 and any siblings) get migrated to the new attribution mechanism. Concretely:

- Identify the rows (`harness IS NULL` or `harness NOT IN ('claude', 'shell')`, or query historical migration log).
- For each: either map to the global synthetic UUID (collapse) or assign harness=shell with the existing row's UUID preserved (keep distinct identity).
- Document which approach is chosen.

## Verification

1. From a non-Claude shell, with no `ENDLESS_SESSION_ID` set:
   - `endless task add "test"` — refuses with a clear error pointing at `--user-session` / `--new-session` / `session start`.
   - `endless task add "test" --user-session` — succeeds; audit trail shows the global synthetic UUID with harness=shell.
   - `endless session start` — prints `export ENDLESS_SESSION_ID=<fresh-uuid>`; eval, then `endless task add "test"` succeeds attributed to that UUID.

2. From inside a Claude session:
   - `endless task add "test" --user-session` — refuses with a clear error: "this flag is for non-Claude shells only; you're inside Claude (CLAUDECODE=1)."
   - `endless session start` — same refusal.
   - `endless task add "test"` (no flags) — works as before, attributed to the Claude session.

3. Migration:
   - Mike's existing session=490 (the snapshot-removal bypass) appears in `sessions` with harness=shell after migration.
   - No orphan sessions remain with NULL/unknown harness.

4. Discovery soundness (precondition verification):
   - Before this task moves to ready: at least N days (Mike picks) with zero session-misattribution incidents reported. The blocking tasks' verification covers this.

## Coordination

- **Blocked-by**: TouchSession-always-real-DB task; E-1407.
- **Relates-to**: E-1401 (the gate this opens an attribution channel for), E-1426 (the architecture this builds on).

## Out of scope

- Multi-user attribution / OS-user-aware synthetic UUIDs. Single global synthetic is enough.
- A `users` table for authorization. Explicit YAGNI.
- TTY auto-detection. Explicit-only for now.
- A bypass that works without one of the new flags / verbs. Closing the current ad-hoc bypass is part of the migration.
