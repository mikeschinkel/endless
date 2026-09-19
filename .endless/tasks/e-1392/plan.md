You are implementing **E-1392** — landing visibility. New event kind, new materialized table, new read CLI. Implements E-1352; follows E-1322 / E-1391 pattern.

## What this is

"Landing" = a task's code actually merged to main. Distinct from:
- `task.status_changed → confirmed` (just the status flip; can happen without code landing)
- `worktree.destroyed` (filesystem cleanup; can happen on drop without landing)

Today there's no way to query "what code did session N actually ship." E-1392 fixes that.

## Three pieces

1. **New event kind**: `task.landed`. Emitted from the worktree-land path in the endless CLI when a branch successfully merges/rebases onto main.
2. **New table**: `session_landings(session_id, task_id, commit_sha, landed_at, branch_name)`. UNIQUE(session_id, task_id, commit_sha).
3. **New CLI**: `endless session landings [--session N]` listing landings attributed to a session.

## Schema (V11 migration)

```sql
CREATE TABLE session_landings (
    id          INTEGER PRIMARY KEY,
    session_id  INTEGER NOT NULL,
    task_id     INTEGER NOT NULL,
    commit_sha  TEXT    NOT NULL,
    branch_name TEXT    NOT NULL,
    landed_at   TEXT    NOT NULL,
    UNIQUE(session_id, task_id, commit_sha)
);
CREATE INDEX idx_session_landings_task ON session_landings(task_id);
```

**ALL tables get a surrogate `id INTEGER PRIMARY KEY` as the first column** — Mike's 35+-year DB rule, no exceptions. The UNIQUE(session_id, task_id, commit_sha) stays as a constraint but is NOT the primary key. E-1322's session_tasks landed without an id PK; that's being fixed in E-1396. Do not repeat the omission here.

NO FK on session_id or task_id per the same rationale as session_tasks/session_worktrees (materialized index must outlive referenced entities).

## Event kind

`internal/events/event.go`:
```go
KindTaskLanded Kind = "task.landed"
```
Register in `ValidKinds`.

`internal/events/payload.go`:
```go
type TaskLandedPayload struct {
    TaskID     int64  `json:"task_id"`
    CommitSHA  string `json:"commit_sha"`
    BranchName string `json:"branch_name"`
}
```

## Handler

`internal/events/executor.go`: `execTaskLanded`. Wire to dispatch.

```go
func execTaskLanded(db dbQuerier, evt *Event) (*ExecuteResult, error) {
    var p TaskLandedPayload
    if err := json.Unmarshal(evt.Payload, &p); err != nil { ... }
    if shouldRecordSessionTouch(evt) {
        // INSERT into session_landings
    }
    return &ExecuteResult{}, nil
}
```

The session_tasks upsert from E-1322 also fires (since task.landed is a task.* event with session_id) — but that records the touch, not the landing. Both fires are correct: session_tasks tracks ALL touches; session_landings is the specific landing record.

## Emit point

`endless worktree land` is implemented in Python (probably `src/endless/worktree_cmd.py`). After the successful merge/rebase to main, emit `task.landed` with the actual commit_sha that landed (not the worktree branch's head before merge — the SHA on main after).

If the land path includes squash-merge, the commit_sha is the squash commit's SHA. If it's a fast-forward, it's the worktree branch tip.

## CLI surface

```
endless session landings [OPTIONS]

Options:
  --session N    Session id (default: current pane)
  --since <when>
  --format [table|json]
```

Output: task ID / commit_sha (short) / branch_name / landed_at / task title (JOIN on tasks).

## Files to touch

- `internal/monitor/db.go` — migrateV11 + version bump
- `internal/events/event.go` / `payload.go` — new Kind + payload
- `internal/events/executor.go` — execTaskLanded + dispatch case
- `internal/events/task_landed_test.go` (new) — handler tests
- `internal/monitor/session_landings.go` (new) — read helper
- `src/endless/worktree_cmd.py` — emit task.landed after successful merge
- `src/endless/session_cmd.py` — new subcommand

## Critical: actor guard

Same as E-1391: `evt.Actor.SessionID != ""`. Do NOT gate on `Kind == ActorSession`. See LESSONS.md "Verify the actor model before specifying actor-based guards."

## E2E verification (critical)

Set up a probe task on a real branch:

```
XDG_CONFIG_HOME=$HOME/.config endless task add "probe E-1392 landing"
# ... (make a trivial change, commit, land)
XDG_CONFIG_HOME=$HOME/.config endless worktree land <probe-task-id>
XDG_CONFIG_HOME=$HOME/.config endless sql "SELECT * FROM session_landings WHERE task_id = <probe-id>"
# Should show one row with the actual merge commit's SHA
XDG_CONFIG_HOME=$HOME/.config endless session landings
# Should list the probe's landing at top
```

## House rules

- `just build`. Binaries → `./bin/`.
- ClearPath, doterr, go-dt.
- In-tx dbQuerier pattern (E-1315 deadlock fix).
- Verify the actual commit_sha on main is captured (not a stale pre-merge SHA).

## Closing

- `endless task update E-1392 --status verify --outcome "task.landed event fires on worktree land; session_landings populates with actual main-side SHA; CLI surfaces history; e2e verified."`
- Handoff: "To verify: land a probe task, then `endless session landings` shows it."
- Do NOT auto-land. Ask Mike.
