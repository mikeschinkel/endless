# E-1530: Fix sessions lookup picking ghost rows on reused pane ids after tmux server restart

## The bug, concretely

`internal/monitor/tmux_lookup.go:88` (`queryActiveTaskForPanes`) joins
sessions to tasks by bare `process` string with no `state` filter:

```sql
SELECT t.id, …
FROM sessions s JOIN tasks t ON t.id = s.active_task_id
WHERE s.process IN (?,?,…) AND s.active_task_id IS NOT NULL
ORDER BY s.last_activity DESC LIMIT 1
```

When a tmux server restarts and the next server reuses a pane id, rows
left over from the previous server's panes — already marked `state='ended'`
by `ReapDeadTmuxPanes` — still match this query because the filter omits
state. Observed: pane `%186` resolved to `E-1337` because of two May-21
sessions (482, 484) whose `process='%186'` predated the current server.

Sibling lookup `GetLiveSessionByProcess` (line 285) already filters
`state != 'ended'` correctly. This is a one-site inconsistency.

## What ships in E-1530

### 1. Read-side state filter (the core fix)

`internal/monitor/tmux_lookup.go:88` — add `AND s.state != 'ended'` to
`queryActiveTaskForPanes`. Audit every other reader of `sessions` that
treats rows as "live" without a state filter; bring them in line.
Documented candidates from this investigation:

- `queryActiveTaskForPanes` — the bug site
- `anySessionForPanes` (`tmux_lookup.go:237`) — `SELECT 1 … WHERE process IN (…)` — same omission
- Any other `WHERE process IN` or `WHERE session_id =` that the implementer finds during audit

### 2. NULL `process` on end — two layers

The invariant: a session in `state='ended'` has `process IS NULL`.
Enforced at two layers.

**Layer A — explicit code-level NULL at every end-of-life write site.**
Documents intent at the call site:

- `internal/monitor/reap.go:52` and `:68` — `ReapDeadTmuxPanes` adds
  `process=NULL` to the same UPDATE that sets `state='ended'`.
- `internal/monitor/session.go:362` — `EndSession` adds `process=NULL`.
- `internal/monitor/session.go:266` — collision-invalidation path
  (note: E-1468 plans to revisit this site's logic; the NULL addition
  is correct independent of that future change).
- Any other ended-write the implementer finds during audit.

**Layer B — schema-level trigger as the invariant enforcer.** Catches
any path that forgets layer A (including future code). Goes in
`internal/schema/schema.sql` and the change file:

```sql
CREATE TRIGGER IF NOT EXISTS sessions_null_process_on_end_update
AFTER UPDATE OF state ON sessions
WHEN NEW.state = 'ended' AND NEW.process IS NOT NULL
BEGIN
    UPDATE sessions SET process = NULL WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS sessions_null_process_on_end_insert
AFTER INSERT ON sessions
WHEN NEW.state = 'ended' AND NEW.process IS NOT NULL
BEGIN
    UPDATE sessions SET process = NULL WHERE id = NEW.id;
END;
```

The inner UPDATE doesn't recurse the AFTER UPDATE trigger because
SQLite's `recursive_triggers` is OFF by default; the `WHEN` clause
would short-circuit it even if recursion were on. The INSERT trigger
covers direct inserts with `state='ended'` (e.g. patterns like
`task_cmd.py:3455`'s INSERT).

Keeping layer A even with layer B is deliberate: explicit NULL in code
makes the write-site honest, and a future schema change that touched
the triggers wouldn't silently lose the guarantee.

### 3. NULL-handling audit for `sessions.process` readers — Go AND Python

Every reader of `sessions.process` must tolerate NULL. The audit
covers both languages because E-1486 ("port all Python SQLite access
to Go") is `ready` but not started — Python still owns ~22 files of
direct SQLite access at planning time.

**Go**: replace `var p string` with `var p sql.NullString` where a NULL
is now reachable. Walk all sites returned by:

```
grep -rn --include="*.go" "sessions" internal/ cmd/ | \
  grep -v _test.go | grep -i "process"
```

**Python**: a `NULL` from SQLite surfaces as `None` in `sqlite3.Row`;
sites that do `row["process"]` and pass the value into string
operations (`.startswith`, `f"{...}"`, equality with a pane id) must
gate on `is not None`. Starting set of sites (from the planning grep
on 2026-06-04, may have moved by implementation time):

- `src/endless/session_cmd.py` lines 69, 81, 91, 283, 321 (SELECTs
  that include `process` in projection or filter)
- `src/endless/task_cmd.py` lines 1951, 2213, 2547, 2557 (session
  joins on `process`)
- `src/endless/task_cmd.py:3455` (INSERT — confirm `process` is set
  explicitly or omitted; either is fine after the trigger)

Most existing call sites already filter on liveness (via state or by
querying a known-live session id), and so won't see NULL in practice.
The audit goal is to confirm — not to retrofit defensive checks where
unreachable.

### 4. Three tmux verbs

#### `endless tmux apply` (existing — unchanged)
Writes tmux config: status-format[1], hotkey/mouse menu bindings,
status-interval. Idempotent. Already correct after E-1529.

#### `endless tmux reset` (new)
DB-side cleanup for the current tmux server. Wraps `ReapDeadTmuxPanes`
for the current project and exposes it as a CLI verb. Useful for
debugging and as the building block for `init`.

Implementation: thin wrapper in `internal/tmuxcmd/reset.go`, dispatched
by `tmux.go:Run()`. Resolves the current project (same logic as `apply`),
calls `monitor.ReapDeadTmuxPanes(projectID)`, prints how many rows were
marked ended. Refuses to run if `TMUX` env is empty (consistent with
`apply`).

#### `endless tmux init` (new)
The target for `~/.tmux.conf`'s `session-created` hook. Self-gates via
the tmux server-level user option `@server_uuid`:

1. Read `tmux show-options -gv @server_uuid` (stderr swallowed; missing
   option returns empty).
2. If empty → fresh server: generate a UUID, run `reset`, run `apply`,
   set `@server_uuid` to the generated UUID via `tmux set-option -g
   @server_uuid <uuid>`. Print a one-line summary.
3. If set → no-op; print "init: server already initialized
   (@server_uuid=…)" to stderr at low verbosity and exit 0.

User-facing surface is one line in `~/.tmux.conf`:

```tmux
set-hook -g session-created "run-shell 'endless tmux init'"
```

No `if-shell` gating in the user's config — the gate is the verb's
responsibility. Manual `endless tmux init` after a server start works
the same way (idempotent: first call does work, subsequent calls no-op
until next server restart).

Implementation: `internal/tmuxcmd/init.go`, dispatched by
`tmux.go:Run()`. UUID generation via `crypto/rand` → standard
8-4-4-4-12 hex form.

### 5. Schema change file: drop `status` column + install trigger

Single change file covers both schema-side concerns for E-1530:

- `internal/schema/changes/e-1530-sessions-cleanup.sql`:
  - `ALTER TABLE sessions DROP COLUMN status;` — the column has zero
    readers in code (verified by grep) and isn't defined in
    `internal/schema/schema.sql`; it's a vestigial column from a
    long-ago schema iteration.
  - The two `CREATE TRIGGER IF NOT EXISTS` statements from section 2
    (NULL-on-end UPDATE and INSERT triggers).

Also update `internal/schema/schema.sql`:

- Add the same two trigger definitions so fresh clones get the
  invariant from the start.
- Do *not* add `status` to the sessions table — it isn't there now,
  and the change file removes it from existing DBs.

SQLite 3.51 (system + bundled) supports `ALTER TABLE DROP COLUMN`. The
dispatcher's `_schema_version` marker prevents re-application of the
change file.

Keep `active_goal_id`, `primary_focus_id`, `current_focus_id` — those
belong to planned future work and are not dropped here.

### 6. One-shot data cleanup (already applied)

`UPDATE sessions SET process=NULL WHERE state='ended' AND process IS
NOT NULL` was run via `endless --db main sql --write` during planning
(412 rows). The sessions table is direct-write (the rebuild projector
explicitly skips session events at `internal/events/projector.go:105`),
so no ledger drift; rebuild won't re-pollute.

The implementer should still confirm `SELECT COUNT(*) FROM sessions
WHERE state='ended' AND process IS NOT NULL` returns 0 before starting
— and re-run the same UPDATE if it doesn't (e.g., the DB was rebuilt
from a stale snapshot, or new ended sessions accumulated between
planning and implementation).

## Tests

- **Regression test for the core bug**: `queryActiveTaskForPanes` with
  a fixture that has both a live session and an ended session sharing
  the same `process` returns the live one. Fails on the current code.
- **`ReapDeadTmuxPanes` NULLs process at the code level**: after reap,
  all newly-ended rows have `process IS NULL`. Verifies layer A
  independently of the trigger.
- **Trigger enforces NULL-on-end (layer B)**: issue a raw `UPDATE
  sessions SET state='ended' WHERE id=?` (no `process=NULL` in the
  SET) against a fixture row with non-NULL process; assert process is
  NULL after the statement returns. Repeat for INSERT with
  `state='ended'` and a non-NULL process literal. This guards against
  someone later removing the trigger or shipping a write site that
  skips layer A.
- **`endless tmux reset` end-to-end**: integration test that seeds a
  ghost row, calls `reset`, asserts the row is marked ended with
  `process IS NULL`.
- **`endless tmux init` UUID gate**: first call does work and sets
  `@server_uuid`; second call (with `@server_uuid` set) short-circuits
  and does no work. Probably driven by a fake `tmux` shim in tests
  since touching the real server is fragile.
- **NULL-handling spot checks**: any reader the audit identified that
  could see NULL gets a unit test passing NULL through. Cover Go
  (`sql.NullString`) and Python (`None` path) sites.

## Out of scope (explicitly)

- **Claude session UUID as primary identity** (E-1173). Independently
  useful for crash recovery; not required for E-1530's fix. Linked via
  `relates_to` so the connection is discoverable.
- **Collision-invalidation false positives** (E-1468). Same root cause
  family (pane id is not stable identity), opposite symptom direction
  (write-side over-eager invalidation marks live sessions ended).
  Independent fix. Linked via `relates_to`.
- **Top-level `endless apply` superset verb**. Considered and rejected
  during planning — the tmux namespace is the right home; subsuming
  into a top-level verb mixes concerns and disrupts the existing
  `endless-go tmux <verb>` dispatcher structure.

## Implementation order (suggested)

1. Read-side state filter in `queryActiveTaskForPanes` + any sibling
   inconsistencies found during audit. Add the regression test first
   (red), then the fix (green). Smallest, highest-confidence change.
2. NULL-handling audit pass for `sessions.process` readers.
3. ReapDeadTmuxPanes (and EndSession + collision-invalidation) also
   NULL process. Add tests.
4. `endless tmux reset` verb + tests.
5. `endless tmux init` verb + UUID-gate tests.
6. Schema-change file dropping `sessions.status`.
7. End-to-end manual verify in a running tmux: `endless tmux init` on
   a fresh server does work and prints a summary; second call no-ops;
   pane lookups return correct task even from sibling panes.

## Verification (handoff message when flipping to verify)

> Implementation complete. To verify:
>
> 1. `endless sql "SELECT COUNT(*) FROM sessions WHERE state='ended' AND process IS NOT NULL"` → 0.
> 2. `endless sql "SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='status'"` → 0.
> 3. From any tmux pane: `endless tmux status-line` returns this window's
>    active task, not a ghost.
> 4. Add `set-hook -g session-created "run-shell 'endless tmux init'"` to
>    `~/.tmux.conf`, restart tmux, confirm status row appears without
>    running `endless tmux apply` manually. Run `endless tmux init` again
>    in a sibling pane; confirm it no-ops with `@server_uuid` already set.
> 5. `tmux show-options -gv @server_uuid` returns a UUID after step 4.

## Relations

- `--relates-to E-1468` — false-positive direction of the same root cause.
- `--relates-to E-1173` — Claude UUID as stable identity; separately useful.
- `--relates-to E-1505` — Claude Desktop sessions also write to this table;
  the NULL-handling audit should confirm Claude Desktop's `process` shape
  (`claude-code_2-1-149_agent`) doesn't interact badly with the state
  filter.
