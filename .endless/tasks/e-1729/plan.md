# E-1729 — Isolate the sandbox ledger

## Problem
`--db sandbox` (E-1281) isolates the DB (`ConfigDir()/endless.db`) but NOT the
event ledger. Every emit resolves its ledger dir from `--project-root` — the
real checkout, which `emit_event` looks up from the projects table — so sandbox
emits append + git-commit event JSONL into the production
`~/Projects/endless/.endless/db-ledger/`. Sandbox/test tasks (ids 1,2,3… under
per-sandbox node ids) therefore permanently pollute the real ledger, collide
with real E-1..E-96, and produce the `UNIQUE tasks.id` / FK noise in
`validate-db`. (Excising the already-leaked events is E-1730, blocked on this.)

## Decision (single path)
Route a sandbox's ledger to a sandbox-local dir and skip its git commit.
**Go-only change; no Python change; no migration.**

- **Sandbox ledger location:** `<ConfigDir()>/.endless/db-ledger/` — same
  `.endless/db-ledger` layout as the real project, but under the sandbox's own
  config dir (in sandbox mode `ConfigDir()` is
  `~/.cache/endless/sandboxes/e-NNN/endless`), a sibling of the sandbox
  `endless.db`.
- **No git commit in the sandbox.** The sandbox dir is not a git repo and is
  disposable — nothing rebuilds from its ledger across machines — so the
  per-segment `git add/commit` (E-1206) is skipped in sandbox mode rather than
  `git init`-ing every sandbox.

## Why not DB-only emits (ruled out)
The DB is a projection of the ledger; a DB write with no ledger event can't be
rebuilt, and `rebuild-db` in a sandbox would wipe all sandbox state. Incompatible
with events-authoritative. The ledger must exist; it just has to be isolated.

## Implementation (all in `internal/eventcmd/event.go`; no events-package signature changes)
Every ledger read/write already funnels through this file — 3 `events.NewWriter`
callsites (incl. the writer reused by `makeDerivedEmitter`) and 2
`events.ProjectToTempDB` callsites (validate-db, rebuild-db) — so the fix is
contained here.

1. Add one helper in `eventcmd`:
   `ledgerRoot(projectRoot string) string` → returns `monitor.ConfigDir()` when
   `monitor.IsSandboxActive()`, else `projectRoot`. (`NewWriter`/`ReadAllEvents`
   append `.endless/db-ledger` to whatever root they get, so the sandbox ledger
   lands at `ConfigDir()/.endless/db-ledger`. `NewWriter` already `MkdirAll`s the
   dir on first write, so the sandbox ledger dir is created automatically.)
2. Pass `ledgerRoot(*projectRoot)` in place of `*projectRoot` at all 3
   `NewWriter` callsites and both `ProjectToTempDB` callsites. This is what makes
   `validate-db --db sandbox` / `rebuild-db --db sandbox` read the SANDBOX ledger.
3. Guard the 3 `CommitLedgerSegment(...)` calls with
   `if !monitor.IsSandboxActive() { ... }`. Real emits commit exactly as today.

No Python change: `emit_event` keeps passing `--project-root` (real checkout) and
`--config-dir <sandbox>` (via `go_db_context_args`). `IsSandboxActive()` is driven
by `ConfigDir()` (which honors `--config-dir`/`XDG_CONFIG_HOME`), so the Go side
detects the sandbox and ignores `projectRoot` for the ledger automatically.

Confirm no ledger callers exist outside `eventcmd` before finishing (grep
`NewWriter`/`ReadAllEvents`/`CommitLedgerSegment`; today they are all here).

## Out of scope / non-goals
- Excising already-leaked events from the real ledger — that is **E-1730**
  (blocked_by this).
- No migration/back-compat: existing sandbox DBs simply begin a fresh (empty)
  sandbox ledger. A stale sandbox with no ledger projects to empty (fine —
  disposable); recreate the sandbox if a populated ledger is wanted.
- The normal `worktree land` ledger path (real, main-pinned) is unchanged.

## Verify (`tests/tasks/e-1729-verify.sh`, fleet convention + go unit)
1. Snapshot the real `~/Projects/endless/.endless/db-ledger/` line count and
   `git rev-parse HEAD`.
2. Run `endless task add "probe" --db sandbox` (and a status change).
3. Assert: (a) a new segment/lines appeared under
   `<sandbox>/.endless/db-ledger/`; (b) the real ledger line count is UNCHANGED;
   (c) the real repo HEAD is UNCHANGED (no commit); (d)
   `endless task show <id> --db sandbox` sees the task; (e)
   `endless-go event validate-db` in the sandbox reads the sandbox ledger.
4. Go unit test for `ledgerRoot`/`IsSandboxActive` (sandbox vs real).
5. `just test` + `go test ./...` green.
