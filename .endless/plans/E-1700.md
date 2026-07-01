# E-1700 — Fix spawned session losing `active_task_id` (NULL → "claim a task")

## Context

A spawned worker session's tmux window shows the `PaneStatusNoTask` hint
("claim a task") even though the task is spawned, the worktree exists, and the
session is actively working it. Confirmed in production: session 845 (E-1699,
pane `%308`) has `sessions.active_task_id` = NULL, while its siblings 844→1695
and 843→1698 are correctly bound. `esu` still works because it resolves via the
session→worktree mapping, not `active_task_id` — hence the split symptom.

The status line (`GetActiveTaskForPane`, tmux_lookup.go:104) keys strictly on
`sessions.active_task_id`; NULL renders "claim a task". This is distinct from
the already-landed status-line *display* fixes — those made the bar read the
correct DB column; this is about that column being NULL when it shouldn't be.

## Root cause (candidate #2 — the never-bound race)

The plan listed two candidate causes. Investigation rules out #1 and confirms #2:

- **Candidate #1 (chat takeover clears it) — ruled out.**
  `monitor.StartChatSession` (session.go:116) does null `active_task_id`, but it
  is only ever called from PostToolUse when a Bash command matches
  `endless task chat` (claude.go:758). A spawned worker never runs that. And the
  clearing is *intended* behavior — pinned by
  `TestStartChatSession_UpsertClearsActiveTask` (session_lifecycle_test.go:218).
  So this path must NOT be changed.

- **Candidate #2 (SessionStart bind race) — confirmed structural hole.**
  On SessionStart the hook binds a spawned session two ways:
  1. Spawn-marker path (claude.go:209-215): `if tmuxSpawnedBy() != "" { if taskID
     := tmuxTaskID() > 0 { BindSessionToTask(...) } }`.
  2. Cwd fallback (claude.go:237-239): `autoBindFromCwd(...)`, **gated on
     `tmuxSpawnedBy() == ""`**.

  `endless task spawn` sets `@endless_spawned_by`, `@endless_task_id`, and
  `@endless_project_id` as three separate synchronous `tmux set -w` calls
  (task_cmd.py:4439-4453) before launching Claude. When the hook reads them via
  `tmux display-message -t $TMUX_PANE` (claude.go:974-994), the `@endless_task_id`
  read can return empty (→ `tmuxTaskID()` = 0) independently of
  `@endless_spawned_by`, or `BindSessionToTask` can error. In that case:
    - the spawn-marker path no-ops (`taskID <= 0`), **and**
    - the cwd fallback is skipped because `tmuxSpawnedBy() != ""`.

  Result: the session is never bound and nothing re-binds it later. 845 lost a
  race its siblings won. `autoBindFromCwd` derives the task from `payload.CWD`
  (always the worktree for a spawned worker — spawn `cd`s into it before launch),
  so it would bind reliably **if it were allowed to run**. The gate condition is
  the bug: it approximates "spawn path already bound" as "spawn marker present,"
  but the marker can be present while the bind still fails.

Note: the hook log does not record best-effort binds or session UUIDs, so no
deterministic per-session trace for 845 exists — the ruling above is from the
code paths + the pinned chat-takeover test, not a log line.

## Fix

Make the cwd fallback a *true* fallback: run it whenever the spawn-marker bind
did not actually happen (marker absent, `@endless_task_id` unreadable, or the DB
write failed) — not merely when the marker is absent. `payload.CWD` is the
reliable signal for a spawned worker, so this closes the race without touching
chat-takeover semantics and without a later-event rebind (a per-event rebind
would fight `StartChatSession` — it would resurrect an intentionally-unbound
chat session whose window still carries `@endless_task_id`).

### Code — `internal/hookcmd/claude.go`

1. **Make the tmux readers stubbable.** Convert `tmuxSpawnedBy` and `tmuxTaskID`
   from `func` declarations to package-level `var … = func() … {…}`. Call sites
   are unchanged; this only opens a test seam.

2. **Extract the spawn-marker bind into a bool-returning helper** (replaces the
   inline block at 209-215):

   ```go
   // trySpawnBind attempts the spawn-marker auto-bind and reports whether the
   // session was actually bound. Returns false when there is no spawn marker,
   // the @endless_task_id window option is not readable (a tmux read race), or
   // the DB write fails — signaling the caller to fall back to cwd-derived
   // binding (E-1700).
   func trySpawnBind(projectID int64, payload claudePayload) bool {
       if tmuxSpawnedBy() == "" {
           return false
       }
       taskID := tmuxTaskID()
       if taskID <= 0 {
           return false
       }
       if err := monitor.BindSessionToTask(payload.SessionID, projectID, taskID); err != nil {
           log.Printf("spawn-bind session %s to task %d: %v", payload.SessionID, taskID, err)
           return false
       }
       return true
   }
   ```

   (This also fixes the silently-discarded `BindSessionToTask` error at the
   current call site — an observability gap surfaced during investigation.)

3. **In the SessionStart branch**, capture the result and change the fallback
   gate from `tmuxSpawnedBy() == ""` to `!spawnBound`:

   ```go
   spawnBound := false
   if payload.AgentID == "" {
       spawnBound = trySpawnBind(projectID, payload)
   }
   // … handleWorktreeAdoption stays where it is (may return early) …
   if payload.AgentID == "" && !spawnBound && os.Getenv("CLAUDE_JOB_DIR") == "" {
       autoBindFromCwd(projectID, payload)
   }
   ```

   Update the fallback's comment to state that it now also covers the spawn path
   whose tmux marker read raced (E-1700).

Behavior is preserved in every case except the bug: no spawn marker →
`spawnBound=false` → fallback runs (unchanged); marker + task id readable →
`spawnBound=true` → fallback skipped, no double-bind (unchanged); subagents /
background agents still screened by `AgentID`/`CLAUDE_JOB_DIR` (unchanged);
marker present but task-id read raced → `spawnBound=false` → **fallback now
binds via cwd (the fix)**. The two bind sites stay separated by
`handleWorktreeAdoption`, so adoption-refusal ordering is unchanged.

### Test — new `internal/hookcmd/spawn_bind_test.go`

- `TestTrySpawnBind_RaceReturnsFalse` (pure, no DB): stub `tmuxSpawnedBy` = `"835"`,
  `tmuxTaskID` = `0`; assert `trySpawnBind` returns `false`. (Returns before any
  DB call, since `taskID <= 0`.) This pins the race condition that must trigger
  the fallback.
- `TestSessionStartBind_CwdFallbackOnSpawnMarkerRace` (DB-backed via the
  `monitor.SetDBContextDir(dir)` + `t.Setenv("XDG_CONFIG_HOME", dir)` seam that
  `claude_bg_session_test.go` already uses): seed a project + task, build a
  worktree layout with a companion (`makeWorktreeLayout` / `writeTestFile`
  helpers already in the package), stub the marker race
  (`tmuxSpawnedBy`="835", `tmuxTaskID`=0), run the exact SessionStart decision
  (`spawnBound := trySpawnBind(...); if !spawnBound { autoBindFromCwd(...) }`)
  with `payload.CWD` = worktree root, then assert `sessions.active_task_id` ==
  the seeded task id. Against the pre-fix gate (`tmuxSpawnedBy() == ""`) this
  same assertion fails (stays NULL) — i.e. it reproduces the bug.

Restore the stubbed vars via `t.Cleanup` (tests must not `t.Parallel()` while the
seam is held, per the existing DB-seam convention).

### Verify script — new `tests/tasks/e-1700-verify.sh`

Ad-hoc per-task verification script, modeled directly on
`tests/tasks/e-1640-verify.sh` (the closest sibling: another session-binding fix
in the monitor/hook layer, verified by driving the real Go tests). Copy its
harness verbatim — the `section` / `report_pass` / `report_fail` / `summary`
helpers, `assert_cmd`, `assert_go_test`, and the `main()` that resolves the repo
root, checks `go` is on PATH, and bootstraps `go.work` via `just go-work-init`
if missing. `chmod +x` it. Checks:

1. **Build** — `go build ./internal/hookcmd/` and `go build ./internal/monitor/`
   compile with the change.
2. **Fix behavior** — `assert_go_test` on the two new tests:
   `TestTrySpawnBind_RaceReturnsFalse` and
   `TestSessionStartBind_CwdFallbackOnSpawnMarkerRace` (the latter is the
   race → cwd-fallback → bound reproduction).
3. **Regression** — `go test -count=1 ./internal/hookcmd/...` and
   `./internal/monitor/...` stay green; explicitly run
   `TestStartChatSession_UpsertClearsActiveTask` as a named check so the
   preserved chat-takeover semantics are visibly asserted.

Output contract (inherited from e-1640): pass/fail per named check, a summary,
exit 0 = ALL PASSED, 1 = any failure, 2 = environment/setup error. Runnable from
anywhere inside the worktree (`esu` cd's in).

## Critical files

- `internal/hookcmd/claude.go` — SessionStart bind sites (209-215, 237-239),
  `tmuxSpawnedBy`/`tmuxTaskID` (1044-1060, 974-994), `autoBindFromCwd` (1069).
- `internal/hookcmd/spawn_bind_test.go` — new regression test.
- `tests/tasks/e-1700-verify.sh` — new per-task verify script (model:
  `tests/tasks/e-1640-verify.sh`).
- Reference (read-only): `internal/monitor/session.go` (BindSessionToTask,
  StartChatSession), `internal/monitor/session_lifecycle_test.go:218` (pinned
  chat-takeover semantics), `src/endless/task_cmd.py:4435-4453` (marker set
  order), `internal/hookcmd/cwd_bind_test.go` (helpers to reuse).

## Verification

The verify handoff Mike runs is exactly:

```
esu
./tests/tasks/e-1700-verify.sh
```

That script builds the affected packages, drives the new fix tests, and re-runs
the hookcmd + monitor suites (including the preserved chat-takeover test), then
prints ALL PASSED or a per-check failure list.

During implementation, before flipping to unverified, the implementer also:

1. Runs `just build` once (Go + templ + tailwind) so `bin/*` is current.
2. Runs `./tests/tasks/e-1700-verify.sh` and confirms ALL PASSED.
3. Sanity-checks the reproduction: temporarily revert the gate to
   `tmuxSpawnedBy() == ""`, confirm `TestSessionStartBind_CwdFallbackOnSpawnMarkerRace`
   fails (active_task_id stays NULL), then restore.

Optional live E2E (from main, after land): spawn a task, let the worker run to
`unverified`, then check `sessions.active_task_id` for the worker row and the
tmux status line in its window — both should reflect the task until the session
actually ends.

## When done

`endless task update E-1700 --status unverified --db main`, then hand off the
verify instructions above (`esu` then `./tests/tasks/e-1700-verify.sh`). Do not
mark `confirmed`/`assumed`. Do not land/drop without asking.
