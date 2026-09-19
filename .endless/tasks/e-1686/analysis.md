# Root-cause diagnosis — live session stuck in `ended`

## Mechanism

`TouchSession` (`internal/monitor/session.go:236`) is the per-hook heartbeat. Its
`ON CONFLICT(session_id) DO UPDATE` branch bumps `last_activity` and re-populates
`process` via `COALESCE(NULLIF(excluded.process,''), sessions.process)` — but
**deliberately never touches `state`** (comment at `session.go:259`: "UPDATE never
touches state"). That rule correctly avoids clobbering `working`↔`idle`, but it also
means an `ended` row is never lifted back to a live state by the session's own
subsequent activity.

`ended` is reachable from several causes, all terminal under the current model:
- `EndSession` (`session.go:511`) — SessionEnd / a child session's `/exit`.
- `ReapDeadTmuxPanes` (`internal/monitor/reap.go`) — pane gone from tmux.
- Collision invalidation in `TouchSession` (`session.go:280-292`) — a *different*
  session_id touches the same pane (this is E-1468's narrow scope).

Once `ended`, there is **no recovery path**: the session keeps firing hooks,
`TouchSession` keeps bumping `last_activity` (and even re-sets `process` from
`TMUX_PANE`), yet `state` stays `ended`.

## Impact

Every reader filters `state != 'ended'` — this filter is load-bearing (E-1530: tmux
reuses `%N` pane ids after a server restart, so ended rows must be excluded). With the
live session stuck `ended`:
- `tmux status-line` (`GetActiveTaskForPane` → `queryActiveTaskForPanes`,
  `internal/monitor/tmux_lookup.go:98-107`) finds nothing → blank bar / "no session" hint.
- `session next` and the E-1681 nav commands fail the same way.
- `task bind` *appears* to work but binds onto the dead row: `execTaskClaimed`
  (`internal/events/executor.go:758`) runs only `UPDATE sessions SET active_task_id=? WHERE id=?`,
  never `state`, so it can't make an `ended` row visible.

This is the root cause of the long-running "session lifetime tracking is unreliable"
symptom: endings are not random, but there is no self-healing from a wrong/stale ending.

## Live evidence (main DB, 2026-06-29)

Session 822 **is the current Claude session** (UUID `b9195ddf-a130-4553-b8e5-de1b5c7c2be3`):

| id | session_id | process | state | active_task_id | last_activity |
|----|-----------|---------|-------|----------------|---------------|
| 822 | `b9195ddf-…` | `%247` | `ended` | 1540 | current |

`process='%247'` + `state='ended'` + current `last_activity` is the tell: it was ended
once (process NULLed), then the same session's later hooks re-populated `process` via
the `COALESCE` while leaving `state='ended'`.

## Fix direction

When a hook arrives for a session whose row is `ended`, revive it to a live state
(`ended` → `idle`/`needs_input`) — activity is proof-of-life. Gate the revival on a
**`session_id` match**, NOT a bare pane match, to preserve E-1530's reused-pane-id
safety (a reused `%N` belonging to a *different* session_id must NOT be revived).

Secondary facet to resolve or at least verify in the same fix: `task bind` resolving to,
and reporting success on, an `ended` session is misleading — it should either refuse or
revive.

## Relationship to E-1468

E-1468 *prevents* one specific false-ending (collision invalidation on a bare pane-string
match, by adding a liveness check). This task adds *general recovery* so that any stale
ending — from any cause — self-heals on the session's next activity. Complementary, not
duplicate; both touch `internal/monitor/session.go`, so coordinate edits.
