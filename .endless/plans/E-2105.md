# Plan — an owning package for the session state vocabulary

Counterpart to `internal/taskstatus` (E-1891), same shape for the same reason.
Pure refactor: no state is added, no behaviour changes.

## 1. `internal/sessionstate`

A `Status` string type and the four members in lifecycle order: `Working`,
`Idle`, `NeedsInput`, `Ended`.

One `groups` map, five rows:

| Group          | Members (in order)                      | Consumer |
|----------------|-----------------------------------------|----------|
| `All`          | working, idle, needs_input, ended       | the vocabulary; validation |
| `Live`         | working, idle, needs_input              | the 29 `state != 'ended'` sites |
| `MayWrite`     | working, idle                           | `hookcmd.sessionMayWrite` |
| `AwaitsHuman`  | idle, needs_input                       | the board's attention ranking |
| `DisplayOrder` | working, needs_input, idle, ended       | `session list`'s sort CASE |

Accessors mirroring `taskstatus` exactly, so the two packages read alike:
`Get`, `Has`, `SQLList`, `Rank`, `Label`, `Glyph`, `Valid`, `Validate`,
`GroupSlug`, `ParseGroup`, `AllGroupSlugs`, `AllGroups`.

Glyphs move from `session_cmd.py` unchanged — `⟳` working, `‖` idle, `?`
needs_input, `␥` ended — plus `⁇` for a state not in the map, matching the
unknown-glyph idiom both existing views already use.

`Live` is the members list, not a negation. Call sites keep reading
`state IN (<Live>)` rather than `NOT IN ('ended')`, so a state added later is
classified rather than silently inheriting "live".

## 2. The transition table

`transitions.go`: rows of `{from, to, trigger}` where `trigger` names the hook
event and the helper that performs the write. Initial rows, from the writers
verified while planning:

| From    | To           | Trigger |
|---------|--------------|---------|
| (none)  | needs_input  | `SessionStart` → `monitor.InitSession` |
| (none)  | working      | `monitor.RegisterSession` / `TouchSession` INSERT |
| any     | working      | task claim → `monitor.BindSessionToTask` |
| any     | idle         | `Stop` → `monitor.IdleSession` |
| any     | ended        | `SessionEnd` → `monitor.EndSession`; the dedup sweep |
| ended   | needs_input  | any hook event → `monitor.TouchSession` revive (E-1686) |

A `Transitions()` accessor and a test pinning the table. The conversion pass in
§5 confirms each row against the site it names and adds any writer it finds;
that is mechanical discovery, not an open decision.

No mermaid, no generated doc, no drift check — see the analysis.

## 3. `endless-go session-state`

`internal/sessionstatecmd`, registered in `cmd/endless-go/main.go` beside
`task-status`, with the same verbs: `groups`, `get <group>`, `has <group>
<state>`, `sql-list <group>`, `rank <group> <state>`, `label <state>`,
`glyph <state>`, `transitions`. No DB access; the vocabulary is compile-time
data.

## 4. `src/endless/session_states.py`

A pass-through client modelled on `src/endless/statuses.py`, holding no
vocabulary and no group list — one-line shellouts that know verb names and
nothing else. Fails closed: if `endless-go` cannot be resolved or is too old to
know `session-state`, it says so and stops. There is no fallback to fall back to;
a hardcoded copy would be the duplicate this deletes.

`SESSION_STATES = get("all")` at module scope, mirroring `TASK_STATUSES`, so
`cli.py`'s `--state` choice is built from the registry.

## 5. The conversion

Go:
- `internal/monitor` — `session.go`, `live_sessions.go`, `session_notices.go`,
  `worktree_inuse.go`, `project_status.go`, and any other site the sweep finds.
- `internal/hookcmd/claude.go` — the local `stateWorking`/`stateIdle`/
  `stateNeedsInput` constants go; `sessionMayWrite` becomes
  `sessionstate.Has(sessionstate.MayWrite, s.State)`.
- `internal/events/executor.go` — the revive CASE.

Python:
- `cli.py` — `--state`'s `click.Choice` from `SESSION_STATES`.
- `session_cmd.py` — the icon map, the legend, and the sort `CASE`, the last
  built from `DisplayOrder`'s rank.

`internal/projectstatuscmd`'s `boardSessionStates` is deliberately NOT replaced
by a group. It is `'working','idle'` because E-1976 withholds the dead
`needs_input` placeholder, which is a temporary exclusion rather than a durable
meaning; E-2091 deletes it. It stays a documented local constant.

## 6. Verification

`.endless/tasks/e-<id>/verify.sh`, sourcing `_harness.sh`:

1. Fail-fast unit gate: the new package's tests, plus `internal/monitor`,
   `internal/hookcmd`, `internal/projectstatuscmd`, and the Python suite.
2. Group membership pinned exactly, in the idiom of
   `taskstatus`'s `TestGroupMembershipIsPinned` — a deliberate change edits the
   table and says so; an accidental one fails.
3. `MayWrite` is asserted to be what the declaration gate admits, so the gate's
   behaviour is pinned to the group rather than to a switch.
4. No session-state literal survives outside `internal/sessionstate` and the
   schema: a sweep over Go, Python and SQL, excluding the package itself,
   comments, and `boardSessionStates`.
5. Python holds no vocabulary: an AST check that `session_states.py` imports no
   `db`/`sqlite3` and declares no state list, and that `session_cmd.py` and
   `cli.py` no longer carry literals.
6. Byte-identical behaviour, which is the point of the task being a pure
   refactor: against a seeded throwaway database, `endless session list` and
   `endless session list --json` render the same rows, glyphs, legend and sort
   order as the pinned expectations captured from the pre-change build.

## 7. Folded in, per ED-1550 rule 2

`src/endless/db.py` carries a second `CREATE TABLE sessions` with
`CHECK (state IN ('working','idle','needs_input','ended'))` — a fifth vocabulary
site, on a table shape missing `process_id`, `epic_id`, `hidden` and the
`report_*` columns the authoritative `internal/schema/schema.sql` defines.

CHECK constraints are banned (`internal/schema/schema.sql`'s header: SQLite
cannot ALTER or DROP one without rebuilding the whole table, which caused
catastrophic data loss; validation lives in application code — see also
ED-1495). The block is guarded by `if not _has_table(conn, "sessions")` and the
Go schema runs on every connection, so it is unreachable as well as wrong.

Mike's call (2026-09-01): delete it here rather than update it. It is evidence
folded into an open task, not a new filing.

## 8. Not in scope

- No fifth state. E-2091 adds `prompted`, and this task exists to make that a
  one-row change.
- No generated lifecycle diagram or drift check.
- No change to `sessions.state` values in the database, and no migration.
- No change to what any state MEANS. Where a group's membership encodes a rule
  that reads oddly — `AwaitsHuman` including `idle` — the rule is preserved as
  found and the reading is left to E-2091.
