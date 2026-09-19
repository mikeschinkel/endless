# Plan — one implementation of "is this worktree in use", called from both paths

## The constraint that shapes everything

**Do NOT reimplement the guards in `drop_worktree`.** There must be exactly one
implementation, called twice.

This is not the usual don't-repeat-yourself preference. The two call sites are
in different languages: reap's guards are Go, inline in
`internal/monitor/reap_worktrees.go:maybeReapWorktree` (the session query at
:206, `hasLiveProcessInDir` at :237), while drop is Python
(`src/endless/worktree_cmd.py:drop_worktree`) and today runs raw git through
`_git_run` without calling into Go at all. There is no `worktree` subcommand on
`endless-go`.

So "reuse reap's check" cannot be satisfied by an import. Read naively it means
porting two checks into Python — two copies, in two languages, drifting
independently, on the code path that already cost two-plus weeks when it was
wrong. Extract instead.

## Step 1 — extract the guards (Go)

New `internal/monitor/worktree_inuse.go`:

```go
type InUseReason string

const (
    ReasonActiveSession InUseReason = "a live session has this task active"
    ReasonLiveProcess   InUseReason = "a process is holding cwd inside the worktree"
)

// WorktreeInUse reports whether anything still depends on this worktree
// directory, and what.
func WorktreeInUse(dir string, taskID int64) (inUse bool, reason InUseReason, err error)
```

Body is the two checks lifted verbatim from `maybeReapWorktree`:

1. `SELECT count(*) FROM sessions WHERE active_task_id = ? AND state != 'ended'`
   — catches a bound Claude session whose cwd has moved out.
2. `hasLiveProcessInDir(dir)` (`lsof -d cwd +D`) — catches any process standing
   in the directory right now.

Keep `hasLiveProcessInDir` a package-level var so the existing reaper tests can
still substitute their stub.

**Fail closed.** Any error returns `inUse = true`. This matches the reaper's
existing stance — it would rather skip a candidate it cannot reason about than
destroy work — and it is the only safe default for a caller about to `rm -rf`.

## Step 2 — rewire the reaper to call it

`maybeReapWorktree` drops its two inline checks and calls `WorktreeInUse`.
Behavior must be unchanged; the existing reaper tests are the proof. If any of
them need editing beyond a call-shape change, the extraction is wrong — stop
and re-read rather than adjusting the test.

## Step 3 — expose it to Python

Add an `endless-go worktree` subcommand in `cmd/endless-go/main.go`, with an
`in-use` verb:

```
endless-go worktree in-use --dir <path> --task <id> [--json]
```

- exit 0 — not in use, safe to remove
- exit 3 — in use; the reason goes to stdout (`--json` for structured output)
- exit 1 — could not determine; treat as in use

**This verb reads the `sessions` table, so it needs DB context.** Register it
with the normal `ConsumeDBContextFlag` handling (the `event` / `session-query`
group), NOT the `PinMainDB` group — and have the Python side thread `--db`
exactly the way `event_bridge.py` already does. Skipping this is the easy bug:
the verb silently answers from the wrong database and reports no live session.

## Step 4 — call it from drop

`drop_worktree` shells to the new verb before removal and refuses on exit 3,
naming the reason. Behind `--force`, as the existing refusals are.

## Step 5 — the guidance half (see this task's analysis)

State the reset-versus-drop decision in the worktree guide section and the spawn
handoff. A gate stops the damage but not the advice; an agent blocked by a guard
still needs to know that reset or rebase is the correct move when history has
diverged but no committed work would be lost.

## Sizing the extraction for the callers that are coming

Two callers today, more shortly — so put it where they can all reach it rather
than somewhere convenient for drop:

- `internal/sandboxcmd/reapguard.go` (E-1904) already computes overlapping
  worktree-safety facts, including `unmergedTaskBranches`.
- E-1902's retention half needs the merge-state predicate too.

Do not refactor those here — out of scope. Just do not choose a home that makes
folding them in later require a second move.

## Verification — `tests/tasks/e-1947-verify.sh`

- Reap behavior is unchanged: the existing `internal/monitor` reaper suite
  passes untouched.
- Drop refuses when a process holds cwd inside the worktree.
- Drop refuses when a non-ended session row has `active_task_id` for the task
  and no process is in the directory — the case `lsof` alone misses.
- Drop still succeeds on a genuinely idle worktree, and `--force` overrides.
- The verb answers against the DB it was pointed at (regression guard for the
  Step 3 trap).
- **Single-implementation assertion:** the `active_task_id ... state != 'ended'`
  query appears exactly once in the tree, and `worktree_cmd.py` contains no
  `lsof`. This is the check that keeps the duplication from growing back.
- Project-wide: `go build ./...`, `go test ./...`, `just test`.
