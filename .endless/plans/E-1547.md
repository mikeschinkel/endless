# E-1547 — Investigate why task add cleared active_task_id from session 614 during E-1537 claim

## 1. Original observation

On 2026-06-07, claimed E-1537 from session 614 (pane %181), ran 8 `task add --parent E-1537` calls plus one `task block`, then released. `sessions.active_task_id` was empty when checked post-release AND the tmux status bar never updated to show E-1537 during the work. Reclaim immediately afterward wrote `active_task_id=1537` and the status-line rendered correctly.

## 2. What's been ruled out

Subsequent investigation in the same session ruled out: `task add` and `task block` do NOT clear `active_task_id` when run from the claimed session. Confirmed via direct testing.

So the bug is one of:
- The original claim never wrote `active_task_id`
- Something else cleared `active_task_id` between the original claim and the post-release check
- Something else made the status-line reader unable to find the populated row (read-side bug rather than write-side)

The "status bar never updated" detail is the strongest signal: this is not "something cleared it later." The claim either never wrote, or wrote to a row the status-line reader couldn't find.

## 3. Ranked hypotheses

### H1 (most likely): pre-fix stale-process ghost row, fixed by E-1530

E-1530 (commit `d505630`, landed 2026-06-07 02:21 AM, ~4h before the incident at 06:35 AM) fixed `queryActiveTaskForPanes` joining sessions to tasks by bare `process` string with no `state != 'ended'` filter, and added schema triggers to NULL out `process` on EndSession. Crucially, the fix only repairs sessions that get NEW writes; an existing pre-fix session row could still hold a stale `process` value matching pane `%181` from an ended prior session.

Mechanism: `execTaskClaimed` UPDATEs `sessions SET active_task_id = ? WHERE id = ?` (numeric PK), which succeeds against session 614. But `queryActiveTaskForPanes` looks up by `s.process IN (%181) AND s.state != 'ended'` — if pane `%181` matched a different sessions row (newer or wrongly-stamped), the status line read a different row whose `active_task_id IS NULL`. Post-release SQL check filtered by pane would also have seen the wrong row.

### H2: lazy-INSERT racer from E-1455 created a duplicate session row

E-1455's `_ensure_claude_session_id` resolver calls `TouchSession` then looks up `id` by `session_id`. If the claim flow resolved a different `session_id` string than the existing row (e.g. env vars producing a different uuid than the session's registered one), the claim's `task.claimed` event wrote to a new row while the status bar read the old one.

### H3: SessionStart returned early via worktree-adoption refusal

If session 614 was a Claude restart during the work session and `handleWorktreeAdoption` returned a refusal, SessionStart returns early — but this happens AFTER the spawn-marker `BindSessionToTask`. Less likely.

### H4: user-error / terminology slip

`_perform_claim_work` creates the worktree first, then conditionally emits `task.status_changed` and `task.claimed`. If worktree creation failed silently, the claim could have returned early without emitting `task.claimed`. Check error-handling paths in `claim_item` and `_perform_claim_work`.

## 4. Investigation procedure

### Step 1: recover ground truth from the event log

```
endless event list --session 614 --since "2026-06-07 06:00" --until "2026-06-07 09:00"
```

Look for: did a `task.claimed` event fire for E-1537 from session 614? Did its executor succeed (any error rows)? Did any later event for that session reference E-1537?

### Step 2: sessions-table forensics

```
endless sql "SELECT id, session_id, process, active_task_id, state, started_at, last_activity FROM sessions WHERE process='%181' OR id=614 OR active_task_id=1537 ORDER BY last_activity"
```

If multiple rows reference `%181` or session 614 with conflicting `active_task_id`, H1 or H2 is confirmed. If session 614 had `active_task_id=1537` at any point in event history, the writer fired correctly and the symptom is read-side only.

### Step 3: schema triggers and the E-1530 fix loaded

```
endless sql "SELECT name FROM sqlite_master WHERE type='trigger' AND tbl_name='sessions'"
```

Confirm `sessions_null_process_on_end_update` and `sessions_null_process_on_end_insert` exist. Their absence in a long-lived DB would explain why H1's pre-fix ghost rows persisted.

### Step 4: reproduce in a sandbox

Spawn a fresh session, claim a throwaway task, run 8 `task add --parent X` calls plus a `task block`, check `active_task_id` after each step, and watch the status line. If the symptom does not reproduce, the writer is healthy in current code.

### Step 5: decide outcome

- Step 1 shows `task.claimed` event fired correctly with the right session_id and no later clearing event → H1 confirmed. Close as "fixed by E-1530; document the latent pre-fix-row symptom in the postmortem."
- Step 1 shows duplicate session rows for the same pane → H2 confirmed. File a follow-up to dedupe.
- Step 1 shows no `task.claimed` event was emitted → H4 confirmed. Look at silent-failure paths in `_perform_claim_work`.
- Step 4 reproduces the symptom in current code → real, current bug. Open a new task with the reproduction recipe.

## 5. Exit criteria

Stop investigating when ANY of these holds:
1. Steps 1 + 2 together explain the historical state (don't need to reproduce).
2. Step 4 fails to reproduce after 2 fresh attempts — close as "no longer reproducible, likely fixed by E-1530 / E-1455 sequence."
3. A concrete current-code bug is identified — convert E-1547 into a fix task or open a child.

**Hard cap: 90 minutes total.** The original report is single-occurrence, the fix landed near in time, and the system has had 8 days of unobserved healthy operation since.

## 6. Critical files

- `internal/events/executor.go` (execTaskClaimed, execTaskReleased)
- `internal/monitor/session.go` (BindSessionToTask, StartChatSession, TouchSession, EnsureClaudeSessionID)
- `internal/monitor/tmux_lookup.go` (queryActiveTaskForPanes, GetPaneStatus)
- `src/endless/task_cmd.py` (claim_item, _perform_claim_work, _current_endless_session_id)
- `internal/hookcmd/claude.go` (SessionStart handler, post-tool detectors)
