# E-698 — Fire-once background job runner

Decisions below were settled with Mike in session before implementation. Where a
decision reverses an earlier proposal, the reversal is noted.

## Scope

A fire-once runner: when invoked it executes any due jobs from a compile-time
job registry, then returns. Scheduling and repetition live in the **trigger**,
not the runner. Ships with **zero production jobs** — the registry is empty in
production and tests register fakes. E-1859 (evaluator) and E-1881 (auto-merge)
are the first real clients.

A general **fault-reporting facility** ships alongside it. It is deliberately
NOT owned by the runner: the runner is its first client, so the hook, land, and
reap can report through the same channel later (E-1884).

## Packages

| Package | Responsibility |
|---|---|
| `internal/faults` | fault recording, incident dedupe, JSONL detail log, code catalog, read API |
| `internal/jobs` | `Job` interface, `Schedule`, registry, `RunDue` (the fire-once runner) |
| `internal/jobscmd` | `endless-go jobs list\|run\|retry` |
| `internal/errorscmd` | `endless-go errors show\|clear` |

New packages follow **ClearPath + doterr**, matching `internal/verify` (the
repo's newest Go package): named returns, `goto end`, sentinels in `errors.go`,
`doterr.NewErr` for wrapping. Older packages (`monitor`, `hookcmd`) are
idiomatic-Go and are not restyled here.

The Go package is named `faults` only because `errors` collides with the
stdlib package name. Every user-facing surface — the table, the CLI verb, the
docs — says **errors**.

## Schema (additive, `internal/schema/schema.sql`)

```sql
CREATE TABLE IF NOT EXISTS jobs (
    name             TEXT PRIMARY KEY,
    next_due_at      TEXT NOT NULL,
    lease_owner      TEXT,
    lease_expires_at TEXT,
    last_run_at      TEXT,
    last_ok_at       TEXT,
    last_error       TEXT,
    run_count        INTEGER NOT NULL DEFAULT 0,
    fail_count       INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now'))
);

CREATE TABLE IF NOT EXISTS errors (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    code          TEXT NOT NULL,   -- 'ERR-0001'
    severity      TEXT NOT NULL,   -- 'warning' | 'error', from the catalog
    source        TEXT NOT NULL,   -- 'job:<name>', later 'hook:PreToolUse', ...
    fingerprint   TEXT NOT NULL,
    summary       TEXT NOT NULL,   -- short descriptive text
    occurrences   INTEGER NOT NULL DEFAULT 1,
    first_seen_at TEXT NOT NULL,
    last_seen_at  TEXT NOT NULL,
    cleared_at    TEXT,
    cleared_by    TEXT
);

-- Incident model: at most ONE open row per fingerprint. A cleared row is
-- immutable history; a recurrence after clearing opens a NEW row so
-- "failed 40x, cleared, came back Tuesday" reads as two incidents.
CREATE UNIQUE INDEX IF NOT EXISTS errors_open_uniq
    ON errors(source, code, fingerprint) WHERE cleared_at IS NULL;
CREATE INDEX IF NOT EXISTS errors_open_idx ON errors(cleared_at, severity);
```

No CHECK constraints (schema.sql house rule). Both tables are new, so the
standalone index statements are safe against pre-existing DBs.

## Fault detail — DB index + JSONL log

The DB row is the **index only**; it is bounded by distinct-fingerprint count,
not by failure count. Every occurrence's **full detail** appends one line to
`ConfigDir()/log/errors.jsonl`, carrying the incident id and occurrence number
so `errors show --detail` can pull it back.

Modeled directly on `internal/monitor/usermachinelog.go` (`user-machine.jsonl`):
append-only, best-effort, all write errors swallowed, `ConfigDir()`-routed so a
sandbox gets its own file. Explicitly **not** the shareable ledger.

**Faults emit no db-ledger events.** They are machine-local observation, not
shareable project history. (Verify: the ledger is driven by the events table,
so plain table writes produce nothing — assert this rather than assume it.)

## Error codes

`ERR-0001`, flat and unprefixed. No subsystem prefix: `JOB-`/`HOOK-` would squat
on namespace that project-scoped task IDs (`EDLS-NNNN`, per Mike) may want. The
`source` column already carries the subsystem.

Codes are `doterr` sentinels in the catalog, each with slug, severity, title and
a `docs/errors.md` anchor. A test gates: every catalog code has a docs section,
every docs section maps to a code, no duplicate numbers.

**Severity lives on the code**, not the call site, so two sites raising the same
condition cannot disagree about whether it is yellow or red.

## The runner

```go
type Schedule struct {
    Interval   time.Duration // normal cadence
    MaxBackoff time.Duration // 0 = no backoff: retry at Interval
    LeaseTTL   time.Duration // 0 = default max(2*Interval, 5m); also the ctx deadline
}

type Job interface {
    Name() string
    Schedule() Schedule
    Run(ctx context.Context) error
}

func Register(j Job)
func RunDue(ctx context.Context) Result
```

**Claim (compare-and-set).** One conditional UPDATE whose WHERE clause is the
mutual exclusion; `RowsAffected == 1` means we own it:

```sql
UPDATE jobs SET lease_owner = ?, lease_expires_at = datetime('now', ?)
 WHERE name = ? AND next_due_at <= datetime('now')
   AND (lease_owner IS NULL OR lease_expires_at <= datetime('now'))
```

Every timestamp is SQLite's `datetime('now')` — the "DB ticker". One shared
clock, so N concurrent monitors cannot disagree about whether a job is due.

**Lease.** Row-level and time-boxed, not an OS lock. A process that dies
mid-run needs no cleanup: the claim lapses and the next invocation re-claims.
Consequence to document on the interface: a merely *slow* job can be re-claimed
after expiry, so **jobs must be idempotent** and `LeaseTTL` must exceed expected
runtime generously.

**Backoff.** On success: `next_due = now + Interval`, `fail_count = 0`. On
failure: `fail_count++`, and when `MaxBackoff > 0`,
`next_due = now + min(Interval * 2^(fail_count-1), MaxBackoff)`; otherwise
`now + Interval`. A continuously failing job therefore decays to the cap — an
effective auto-disable, without ever going silently dark.

**Robustness — the monitor must never fall back to the CLI.**
- `recover()` per job, converted to a fault. An unrecovered panic in a job
  goroutine would kill the whole monitor process.
- Per-job `context` deadline = `LeaseTTL`. Honest caveat: Go cannot kill a
  goroutine that ignores `ctx`; a wedged job leaks one goroutine. Wedged is
  survivable, panicking is not.
- **The standard logger is swapped to a sink for the duration of the tick.**
  `log.Printf` writes to stderr; the monitor paints stdout with cursor-home
  escapes, so a log line lands on top of the drawn table — and since
  `monitorLoop` only repaints when the frame *changes*, the corruption persists
  indefinitely on an idle dashboard. 42 pre-existing call sites are converted
  separately (E-1884); this contains the seam E-698 creates.
- Every DB error inside the runner is recorded and swallowed, never propagated
  to the render path. The render path tolerates the tables being absent.
- The fault-recording path itself must not recurse when it fails.

## Trigger

`monitorLoop` fires `jobs.RunDue()` on a goroutine each 2s tick, behind a
single-in-flight `atomic.Bool` so a slow job never stacks up or blocks the
redraw. `endless-go jobs run` exists for E-1848's daemon, manual runs, and the
verify script.

## DB context — no bespoke rule

**Reversal.** An earlier proposal added a jobs-specific guard suppressing the
runner inside a self_dev worktree. Mike's constraint: don't proliferate
context rules users must learn. The established self_dev rule already exists
(E-1685, and every verify script): a worktree drives the candidate binary
against **its own sandbox**. `session status` / `session monitor` on their tmux
path were the exception — `PinMainDB()` at `session_status.go:165` overrides the
sandbox and reads the real DB.

So instead: **`sessionstatuscmd` skips `PinMainDB` when cwd is a self_dev
worktree**, and the runner gets **no special rule** — it uses the resolved DB
context like every other command. In a worktree that resolves to the sandbox,
which the worktree binary owns, so E-1818 schema-passivity does not apply and
the new tables are created normally.

Known cost, accepted: in a worktree the dashboard will not pane-resolve a focal
task, because the sandbox-seeded session row has no `process` and only the hook
(which pins main) ever sets one. It shows the "no session" hint instead. Mike
does not run the dashboard from worktrees; this is strictly better than silently
reading the real DB. The underlying machine-vs-project split is **E-1883**.

## User-facing surface

```
endless errors show [--all] [--detail]   # open incidents; --all includes cleared
endless errors clear [<id>...|--all]     # marks cleared, never deletes
endless jobs list                        # registry + schedule + last run + fails
endless jobs run                         # fire once, now
endless jobs retry <name>                # "I fixed it": next_due = now, fail_count = 0
```

`errors clear` and `jobs retry` are deliberately separate verbs: clearing means
"I have seen this", retrying means "I have fixed this". Fusing them would let an
acknowledgement silently re-arm a job that is still broken.

Python exposes these as thin delegating wrappers threading the `--db` context,
mirroring `session_status_resolve` in `src/endless/session_cmd.py`.

## The badge

`renderSnapshot` appends a trailing line when open incidents exist — rendered by
**both** `session status` and `session monitor`, since they share the renderer
and the one-shot is the more frequently seen surface. Max severity wins
(error > warning), with per-severity counts, the latest short text, and the
`endless errors show` affordance. Respects the existing `color` flag; no ANSI
when not a TTY. `eraseEachLineToEOL` handles the extra line unchanged.

## Verification

`tests/tasks/e-698-verify.sh`, fail-fast, folding this task's own tests in:

1. Go unit tests for `internal/jobs` and `internal/faults` (fail fast).
2. CAS claim under real concurrency: N processes race one due job, exactly one
   wins.
3. Lease expiry re-claim; a killed owner's job is re-claimable.
4. Backoff math, and reset on success.
5. Panic in a job is recovered, recorded as a fault, and does not kill the run.
6. Incident dedupe: repeats bump `occurrences`; clear-then-recur opens a NEW row.
7. JSONL detail written and readable back; no db-ledger event emitted.
8. Catalog/docs sync gate.
9. End-to-end through the CLI: `jobs list`/`run`/`retry`, `errors show`/`clear`.
10. Badge renders in both `session status` and `session monitor` frames.
11. `session-status` no longer pins main inside a self_dev worktree.

## Deltas from the plan as implemented

- **Guide coverage written, not deferred.** `jobs` and `errors` are new
  user-facing verbs, so `just guide-check` (a pre-land gate) failed with "no map
  file at all". Rather than declare a `gap:` — the established escape hatch, used
  by seven existing commands — both got real sections in `docs/guide/reference.md`
  plus map files, and the generated cross-reference was regenerated.
- **The Python wrapper reuses `event_bridge._resolve_endless_go`.** A first pass
  used `shutil.which`, which finds the GLOBAL binary — the new subcommands exist
  only in the candidate build, so every `endless jobs …` call failed with
  "unknown subcommand" until it landed. The existing resolver already prefers
  `<worktree>/bin/endless-go` under `--db sandbox` (E-1510) for the same class of
  reason.
- **A planned test was wrong and was replaced.** "Faults emit no db-ledger
  events" was drafted as a query against an `events` TABLE. There is no such
  table — the ledger is an append-only directory of segment files
  (`internal/events/writer.go`). The invariant is now asserted two ways that are
  actually true: recording a fault creates nothing but `errors.jsonl`, and
  `internal/faults` has no build dependency on `internal/events`.
- **Reap was NOT migrated**, as decided. It remains fired opportunistically from
  five hook sites plus land/claim. Its migration is a candidate for the runner
  once a real job exists to model it on.

## Filed during this task

- **E-1883** (brainstorm) — db-ledger is project-scoped but `endless.db` is
  machine-global, so a rebuild loses other projects. Second, independent driver
  found here: session-status's main-pin vs sandbox-routed tasks is the same
  machine-vs-project split.
- **E-1884** — convert the 42 `log.Printf` call sites to the fault system.
