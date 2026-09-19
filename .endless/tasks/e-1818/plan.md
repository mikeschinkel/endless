# Fix: an unlanded worktree binary must not apply schema/migrations to the real DB

## Invariant
Only a database's *owning* binary applies `schema.SQL` (DDL + seed) and the enum
integrity gates to it. A candidate (self-dev worktree) binary that has been
pinned to the real DB for session-state writes opens it **schema-passive**: it
uses the deployed schema as-is and never mutates structure or seed rows.

## Where
`internal/monitor/db.go`, `DB()`. The real-DB pin is signalled by
`dbPathOverride != ""`, set by `ForceRealDB()` (the Claude hook) and
`PinMainDB()` (endless-channel, endless-tmux) — every automatic entry point that
redirects a sandbox/worktree-context binary onto `~/.config/endless/endless.db`.

## Change
1. In `DB()`, gate the `schema.SQL` exec (currently ~line 455) and BOTH integrity
   blocks — `tasktype.VerifyIntegrity` (~466) and `sessionkind.VerifyIntegrity`
   (~477) — behind a single guard. Add a named helper for readability, e.g.
   `func pinnedToForeignRealDB() bool { return dbPathOverride != "" }`, and
   comment it with the invariant above.
   - `dbPathOverride == ""` (deployed global binary, or a self-detected sandbox
     open of a DB the binary owns): apply `schema.SQL` + both integrity checks
     exactly as today — unchanged. This is how additive migrations propagate on
     `just install` and at land, and how the sandbox stays current.
   - `dbPathOverride != ""` (pinned to the real DB by ForceRealDB / PinMainDB):
     skip the `schema.SQL` exec and skip both `VerifyIntegrity` calls. Open
     against the existing, deployed-managed schema.
2. Keep the per-connection PRAGMAs (WAL, busy_timeout, foreign_keys) on both
   paths — those configure the connection, they do not mutate schema.

## Tests (internal/monitor)
1. `schema-passive on real-DB pin`: seed an on-disk DB with `task_types` rows
   that DIFFER from the running enum (e.g. id=1 slug `task` while the enum emits
   `todo`); take the pin (call `ForceRealDB()` with `IsSandboxActive()` true, or
   `PinMainDB()`) targeting it; open `DB()`. Assert (a) no error is returned (no
   fail-close) and (b) the on-disk `task_types` rows are byte-for-byte unchanged
   (no `schema.SQL`/seed reconcile ran).
2. `owner path still migrates + verifies`: with no pin (`dbPathOverride == ""`),
   a fresh DB gets `schema.SQL` applied (tables + seed present) and a drifted
   enum still fail-closes — current behavior preserved.
   Use `internal/monitor/test_seam.go` to save/restore `dbPathOverride` /
   `dbContextDir` and isolate the package-global state.

## Verification (single command)
Fold everything above into `tests/tasks/e-1818-verify.sh` (model:
`tests/tasks/e-1659-verify.sh`) — a fail-fast script that:
1. builds `internal/...` + `endless-go`;
2. runs the two `internal/monitor` unit tests above;
3. runs the end-to-end schema-passive check: build a worktree `bin/endless-go`
   whose `tasktype` enum diverges from the real DB, take the pin (ForceRealDB /
   PinMainDB) and open `DB()`, and assert the real DB's `task_types` is
   byte-for-byte unchanged AND the open returns success (no fail-close) — the
   exact scenario that corrupted the real DB during E-1659;
4. asserts the unpinned/owner path is intact: a fresh DB still gets `schema.SQL`
   applied and a drifted enum still fail-closes (so land-time migration via
   `apply-change` is provably unaffected);
5. runs the full Go + Python suites as the final regression.
Hand-off verify command: `esu && ./tests/tasks/e-1818-verify.sh`.

## Out of scope
Data-level writes from a buggy worktree hook (malformed session rows) — a
separate, lower-stakes concern. This fix closes only the structural / schema /
enum escape: an unlanded binary can no longer alter a real DB it does not own.
