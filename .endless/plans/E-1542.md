# E-1542 — Implement pause-on-revisit hook for sessions on epic-descendant tasks

## 1. The behavior

When an operator flips an epic to `status=revisit`, every session whose claimed task is a descendant of that epic must be intercepted at its next tool call. The hook blocks the tool and returns `additionalContext` (a system-reminder-wrapped instruction) telling Claude to surface an AskUserQuestion with two options. Based on the user's answer, Claude calls one of two new verbs (`endless task continue` or `endless task pause`) which clear the gate and apply the choice.

Mechanism = PreToolUse hook returning JSON with `decision: "block"` plus `additionalContext` (NOT `endless channel`, which is too constrained for this affordance per Mike 2026-06-05). The system-reminder framing of `additionalContext` is structured signal Claude reads as instruction, more reliable than stderr text which Claude parses as a tool error.

## 2. Schema: new `session_gates` table (reuses the freed-up name)

E-1582 (blocking prereq for this task) drops the existing pivot-gate `session_gates` table. This task creates a new `session_gates` table designed generically from the start, with a `kind_id` discriminator (per ED-1506: Go enum source-of-truth + values mirror table) and kind-specific named subject columns. The `revisit` kind is the only seeded value at v1; future gate kinds add new rows to `gate_kinds` and new subject columns to `session_gates`.

Polymorphic `subject_id INTEGER` was considered and rejected: it would lose FK referential integrity (no `ON DELETE CASCADE` when the subject row is removed). Named subject columns preserve FK integrity per kind at the cost of accumulating columns. The accumulation cost equals the migration cost of adding columns later, so doing it now isn't speculative beyond `kind_id` itself.

```sql
CREATE TABLE gate_kinds (
    id INTEGER PRIMARY KEY,
    slug TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);

INSERT OR IGNORE INTO gate_kinds (id, slug, label) VALUES
    (1, 'revisit', 'Revisit');

CREATE TABLE session_gates (
    id INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    kind_id INTEGER NOT NULL REFERENCES gate_kinds(id),
    epic_id INTEGER REFERENCES tasks(id) ON DELETE CASCADE,
    triggered_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    cleared_at TEXT,
    cleared_by TEXT
);

CREATE INDEX session_gates_open ON session_gates(session_id, kind_id) WHERE cleared_at IS NULL;
```

For the `revisit` kind: `kind_id = 1`, `epic_id` set, other future subject columns NULL. Open row = pending revisit-prompt for `(session_id, kind_id=revisit, epic_id)`. `cleared_by` values: `revisit_continue`, `revisit_pause`, `revisit_resolved` (epic flipped out of `revisit` before user responded), `superseded`. At most one open row per `(session_id, kind_id, epic_id)` enforced via the partial index plus application-level supersede on insert.

Schema files:
- `internal/schema/changes/e-1582-drop-pivot-gates.sql` (created by E-1582, drops the old pivot-gate `session_gates`)
- `internal/schema/changes/e-1542-session-gates.sql` (created by this task, defines `gate_kinds` + new `session_gates`)
- Mirror both into `internal/schema/schema.sql`.

### Go-side enum

Per `feedback_go_enum_pattern` and ED-1506:

```go
type GateKind int

const (
    GateKindRevisit GateKind = 1
)

func (k GateKind) String() string {
    switch k {
    case GateKindRevisit: return "revisit"
    default: return fmt.Sprintf("GateKind(%d)", int(k))
    }
}

func ParseGateKind(s string) (GateKind, error) {
    switch s {
    case "revisit": return GateKindRevisit, nil
    default: return 0, fmt.Errorf("unknown gate kind: %q", s)
    }
}

func AllGateKinds() []GateKind {
    return []GateKind{GateKindRevisit}
}
```

Lives in `internal/gatekind/gatekind.go`, parallel to `internal/tasktype/tasktype.go`. Startup integrity check verifies `AllGateKinds()` matches the `gate_kinds` table rows by id and slug — same pattern as TaskType.

## 3. New verbs

Two CLI verbs in `src/endless/task_cmd.py` next to `release_item`:

- `endless task continue` — clears the open revisit gate for the current session via `cleared_by='revisit_continue'`. Session resumes; subsequent tool calls pass the PreToolUse check. No other side effects.
- `endless task pause` — clears the open revisit gate via `cleared_by='revisit_pause'` AND calls `release_item` for the active task (worktree stays intact per existing release semantics).

Both verbs look up the session id via the existing `_current_endless_session_id` resolver and the open gate via `session_gates WHERE kind_id = (slug 'revisit')`. If no open gate exists, both verbs emit a friendly "no pending revisit prompt" message and exit 0.

## 4. The hook

In `internal/hookcmd/claude.go handlePreToolUse`, between `enforceWorktreeGate` and the existing pivot-gate check (which will go away with E-1582):

1. Early-return for Bash commands matching `endless task continue` or `endless task pause` regex — the user's clearing action must not be blocked.
2. Resolve the session's `ActiveTaskID`. If nil, skip (no claimed task, nothing to gate).
3. If a pending revisit gate exists for `(session_id, kind=revisit)`, re-check the epic's current status:
   - If the epic is no longer in `revisit`, clear the gate with `cleared_by='revisit_resolved'` and proceed (no block).
   - Else block by returning the JSON response from §5.
4. Else walk ancestry from `ActiveTaskID` looking for the nearest epic in `revisit` status (recursive CTE on `tasks` with depth cap 32). If found, `INSERT INTO session_gates (session_id, kind_id, epic_id, ...) VALUES (..., GateKindRevisit, ...)` and block by returning the JSON response from §5.

The hook fires for **all tool kinds**, not just Write/Edit. The "next tool invocation" language supports this; Read/Bash/Grep should also trigger the pause.

### Implementation-time verification needed

The combination of `decision: "block"` plus `additionalContext` on `PreToolUse` is not explicitly documented in Claude Code's hook docs at the time of writing — the JSON structure (both fields under one `hookSpecificOutput` object) implies they coexist, but the interaction must be verified at implementation time. **Fallback**: if the combination doesn't work (e.g., block dominates and `additionalContext` is discarded), revert §5 to the stderr-message form used by existing endless hooks (worktree-enforcement, commit-on-main). The fallback is functionally equivalent at lower reliability.

## 5. Hook response

The hook returns JSON on stdout:

```json
{
  "decision": "block",
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "additionalContext": "Your active task E-<child> is a descendant of epic E-NNN, which the operator just set to status=revisit. The strategy under which this task was planned is being reconsidered. Surface this to the user as an AskUserQuestion with two options:\n\n  - Continue under the current plan (then call `endless task continue`)\n  - Pause until the strategy is re-set (then call `endless task pause`)"
  }
}
```

The `additionalContext` string is wrapped in a system reminder and injected into Claude's conversation at the point the hook fired. This is structured "instruction context" Claude reads as guidance, not stderr error output Claude parses heuristically. The explicit AskUserQuestion direction routes Claude to the user-question tool rather than guessing or proceeding.

**Fallback (if block + additionalContext interaction doesn't work as expected)**: revert to the prior design — exit code 2 with the same instruction text on stderr. Functionally equivalent at lower reliability; the existing worktree-enforcement and commit-on-main gates use this pattern.

## 6. Monitor helpers

New file `internal/monitor/session_gate.go` (revisit-kind helpers; other kinds will add their own helpers in the same file as they emerge):

- `NearestRevisitEpicAncestor(taskID int64) (epicID int64, found bool, err error)` — recursive CTE walking parent_id chain, filtering for `type_id = TaskTypeEpic AND status = 'revisit'`, returning the nearest match.
- `SetRevisitGate(sessionID int64, epicID int64) error` — inserts; supersedes any prior open row for the pair via `cleared_by='superseded'`.
- `PendingRevisitGate(sessionID int64) (epicID int64, found bool, err error)` — finds the open row for the session.
- `ClearRevisitGate(sessionID int64, epicID int64, clearedBy string) error` — sets `cleared_at = now()`, `cleared_by`.
- `GetTaskStatus(taskID int64) (string, error)` if not already present.

## 7. Tests

Three layers:

**`internal/monitor/session_gate_test.go`** (unit):
- `NearestRevisitEpicAncestor` cases: no epic ancestor, epic ancestor not in revisit, single revisit epic, two revisit epics (nearest wins), cycle (depth cap kicks in).
- `SetRevisitGate` / `PendingRevisitGate` / `ClearRevisitGate` round trip; supersede-prior-open semantics.

**`internal/hookcmd/claude_revisit_test.go`** (hook-level integration):
- Dispatch a synthetic PreToolUse payload with a session bound to a leaf task whose epic ancestor is in `revisit`; assert the hook returns JSON with `decision: "block"` and `additionalContext` containing the AskUserQuestion instructions (or, if the fallback is in use, exit 2 with the instruction text on stderr).
- Verify the `decision: "block"` + `additionalContext` combination actually fires both effects in the live Claude environment (this is the implementation-time verification flagged in §4).
- Simulate `endless task continue` post-tool-use; re-dispatch a Write payload; assert exit 0.
- Epic flipped out of `revisit` between gate-set and next tool call → gate auto-clears with `revisit_resolved`, tool passes.

**`tests/test_revisit_verbs.py`** (Python CLI):
- `endless task continue` clears the gate row.
- `endless task pause` clears the gate AND releases the task.
- Both verbs no-op with friendly message when no gate is open.

## 8. Implementation sequence

1. Schema change: `internal/schema/changes/e-1542-session-gates.sql` + mirror into `schema.sql`. Includes both `gate_kinds` and `session_gates`. Depends on E-1582's drop migration landing first.
2. Go enum in `internal/gatekind/gatekind.go` (parallel to `internal/tasktype/`).
3. Startup integrity check (parity with TaskType).
4. Monitor helpers in `internal/monitor/session_gate.go`.
3. Wire the hook into `handlePreToolUse` between worktree-gate and the pivot-gate check.
4. Wire the early-return for `task continue`/`task pause` Bash commands at the top of `handlePreToolUse`.
5. Add Python verbs in `src/endless/task_cmd.py` + CLI registration in `src/endless/cli.py`.
6. Tests per §7.

## 9. Out of scope

- Cross-package status-changed event subscriber that clears all gates when an epic leaves `revisit` (the in-hook re-check is enough for v1).
- UI badges for "PAUSED (epic-revisit)" in `task next` / `task show` / dashboards — file separately if needed.
- The pivot-gate logic this hook sits alongside is being removed by E-1582.

## 10. Critical files

- `internal/hookcmd/claude.go`
- `internal/monitor/session_gate.go` (new)
- `internal/gatekind/gatekind.go` (new)
- `internal/monitor/task.go`
- `internal/schema/schema.sql`
- `internal/schema/changes/e-1542-session-gates.sql` (new)
- `internal/tasktype/tasktype.go` (uses existing TaskTypeEpic = 4)
- `src/endless/task_cmd.py`
- `src/endless/cli.py`


