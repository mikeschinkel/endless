# E-1506 — Bring Go tests to best practice across the repo (Path A)

## Context

`internal/monitor` is built around a `sync.Once` singleton DB (`monitor.DB()`). Every public wrapper — `TouchSession`, `TaskText`, `BindSessionToTask`, etc. — first calls `DB()` then runs SQL. The singleton has no test-reset, so tests cannot exercise the public surface directly. The current workaround is a private-helper pattern (`touchSessionDB`) that duplicates code and leaves the public wrapper itself untested. `monitor.TaskText` (E-1445, called by `endless-go session-query task-text`) has zero coverage.

E-1473 added the missing packages to `just test-go`, but the tests themselves stop short of the actual production surface. E-1506 closes that gap **for every Go package**, not just monitor — Mike's goal is best-practice test coverage across the codebase, not a narrow point-fix.

## Audit findings (initial pass — see Phase 1 for full sweep)

| Package                       | Pattern                                                              | Status                                                |
|-------------------------------|----------------------------------------------------------------------|-------------------------------------------------------|
| `internal/monitor`            | `sync.Once` singleton + private `*DB` helper carve-out               | ~25 public wrappers untested; private-helper present  |
| `internal/events`             | `dbQuerier` interface — dependency injection                          | Mostly clean; subagent confirms remaining gaps        |
| `internal/kairos`             | Pure logic, no DB                                                    | Mostly covered; subagent confirms                     |
| `internal/hookcmd`            | Mixed; some regex/string unit tests, several DB-touching paths       | Audit needed                                          |
| `internal/sandboxcmd`         | `go build` + `exec.Command(bin, ...)` integration tests              | Clean — already the binary-level pattern              |
| `internal/sessionquerycmd`    | **No tests** — hosts `task-text` and `list-live` verbs               | Add binary tests for both verbs                       |
| `internal/eventcmd`           | Calls `monitor.DB()` in several places — audit needed                | Audit needed                                          |
| `internal/channelcmd`, `tmuxcmd`, `servecmd` | Top-level mains, may have logic worth testing         | Audit needed                                          |
| `internal/web`                | 11 query functions in `queries.go`, all hit singleton                | No tests                                              |
| `internal/config`             | `merge_test.go` only                                                 | Audit for completeness                                |
| `internal/schema`             | Schema SQL data — tested transitively by monitor baseline tests       | Likely fine                                           |

## Approach

Three mechanisms, all already precedented in the repo:

### 1. Singleton test seam in `internal/monitor` (white-box)

Add a `_test.go`-only helper in `baseline_test.go`:

```go
// withTestDB rebinds monitor.DB()'s singleton to a fresh schema-applied DB
// for the lifetime of t. Restores prior state on cleanup so tests run
// sequentially without dbOnce leaking. Use this for any test exercising
// a public monitor.* wrapper that calls DB() internally.
func withTestDB(t *testing.T) *sql.DB {
    t.Helper()
    db := freshDB(t)
    applySchema(t, db)

    prevOnce, prevConn, prevErr := dbOnce, dbConn, dbErr
    dbOnce = sync.Once{}
    dbConn = db
    dbErr = nil
    dbOnce.Do(func() {}) // mark consumed so DB() returns dbConn directly

    t.Cleanup(func() {
        dbOnce, dbConn, dbErr = prevOnce, prevConn, prevErr
    })
    return db
}
```

Test files are `package monitor` (white-box), no exported surface needed.

### 2. Binary-level integration tests for command packages

Mirror the sandboxcmd pattern: `go build cmd/endless-go` into TempDir, point `XDG_CONFIG_HOME` at a seeded DB, run the subcommand, assert stdout/exit. Apply to `sessionquerycmd` and to any `*cmd` package whose Run() can't reasonably be unit-tested in isolation.

### 3. White-box DB injection or seam for `internal/web` queries

`web.queries.go` functions all call `monitor.DB()`. Two options the audit will resolve:
- Use the same `withTestDB` seam from monitor (web tests live in `package web`, so they'd need monitor to expose it — adds a thin `monitor.TestSetDB` in a `monitor_export_test.go` consumed via a `monitor/internal/testing` shim, or)
- Refactor `queries.go` to take a `*sql.DB` argument (cleaner long-term but changes production callers in `web/handlers.go`).

I'll bring the recommendation back to Mike after the audit finishes; both are reasonable.

## Phases

### Phase 1 — Full repo audit (Explore subagent)

Single Explore agent surveys every `internal/*` and `cmd/*` package. Deliverable: structured list of untested public functions per package, plus notes on any private-helper patterns or singleton bindings. I review findings against Mike before committing to per-package scope.

### Phase 2 — Mechanism (me, hands-on)

- Add `withTestDB` to `internal/monitor/baseline_test.go`.
- Inline `touchSessionDB` body back into `TouchSession`; delete the helper.
- Convert five `TestTouchSession_*` tests to call `TouchSession` via the seam.
- Add `TaskText` unit tests in `internal/monitor/task_test.go`.
- Add binary integration tests for `endless-go session-query task-text` AND `list-live` in `internal/sessionquerycmd/task_text_test.go` + `list_live_test.go`. Helper builds `cmd/endless-go` once per test file.
- Verify `just test-go` is green before delegating Phase 3.

### Phase 3 — Backfill per package (general-purpose subagents, one per package)

For each package in the audit findings, dispatch a focused subagent:

- **monitor** wrappers (largest): subagent writes tests for the ~25 untested public wrappers, grouped logically (session lifecycle, channel registry, task readers, project lookup, activity). Uses `withTestDB` seam.
- **web**: subagent writes tests for `queries.go` functions per the seam choice from Phase 1.
- **eventcmd**, **channelcmd**, **tmuxcmd**, **servecmd**, **hookcmd**: subagent audits + writes unit tests for pure logic and binary tests for verbs/dispatch where appropriate.
- **events**, **kairos**, **config**, **schema**: subagent fills any gaps surfaced by Phase 1.

Each subagent gets:
- The seam example (concrete code).
- The list of functions to cover (from Phase 1).
- The assertion conventions used in the existing tests (`t.Fatalf` for setup, `t.Errorf` for assertions, table-driven where the cases vary by input).
- A budget hint: ~2-4 test cases per function unless the function has obvious branching.

Subagent returns: list of files added/changed and a diff summary. I read each touched file, integrate, run `just test-go`, fix any reds.

### Phase 4 — Verification & landing

- `just build && just test-go` green.
- `go vet ./...` clean.
- `grep -rn touchSessionDB internal/` empty.
- Update E-1506 to `verify`, hand off.

## Concrete first-cut files (Phase 2)

- `internal/monitor/baseline_test.go` — add `withTestDB` helper, update package doc.
- `internal/monitor/session.go` — inline `touchSessionDB` into `TouchSession`, delete helper.
- `internal/monitor/session_test.go` — convert five `TestTouchSession_*` tests.
- `internal/monitor/task_test.go` — **new**, `TaskText` unit tests.
- `internal/sessionquerycmd/task_text_test.go` — **new**, binary integration tests for task-text.
- `internal/sessionquerycmd/list_live_test.go` — **new**, binary integration tests for list-live.

Phase 3 files determined by audit; expect roughly:
- `internal/monitor/{session_lifecycle,channels,task_readers,projects,activity}_test.go` — new per-cluster.
- `internal/web/queries_test.go` — new.
- Additional `*_test.go` in eventcmd / channelcmd / tmuxcmd / servecmd / hookcmd as the audit dictates.

## Context-management strategy

- Phase 1: one Explore agent (read-only, returns condensed findings, doesn't bloat my window).
- Phase 2: I work directly — small, mechanism-critical.
- Phase 3: one general-purpose agent per package, each returning a diff summary. I read the actual touched files (small windowed reads), integrate, move on.
- Iteration: full sweep runs after each package; failures are addressed inline.

## Risks & escape hatches

- **Audit may surface bigger uncovered logic** in `eventcmd` / `web` / `channelcmd` than expected. If so, I'll bring findings back to Mike before grinding through.
- **Some functions may need fixture data** that's painful to seed (transcript files, real tmux env). For those I'll also bring findings back to Mike before grinding through. I will provide pros and cons of different approaches for Mike to decide including binary integration tests with a stubbed environment, unit tests even though they may require heavy setup, and/or other potential options.
- **Parallel test execution**: the monitor singleton precludes `t.Parallel()` for tests using the seam. This is fine — `just test-go` already runs sequentially in package order.

## Verification

```sh
just build
just test-go
go vet ./...
grep -rn "touchSessionDB" internal/    # must be empty
```

Acceptance: `just test-go` green, no private DB-injecting helpers in monitor, monitor public wrappers + sessionquerycmd verbs + web queries + cmd-package logic all have at least baseline test coverage. Audit deliverable archived in the verify message so Mike has a record of what changed where.

## Hand-off

- `endless task update E-1506 --status verify --db main` after `just test-go` passes.
- Reply to Mike includes the test commands, the audit summary, and the return line.
