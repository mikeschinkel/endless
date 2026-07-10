Researched 2026-06-24 against the 744-row sessions table.

ROOT CAUSE: dedup key is sessions.session_id (Claude UUID), not active_task_id. Every Claude launch (resume / respawn / aborted spawn / /clear) has a new UUID, so SessionStart's upsert finds no conflict and INSERTs a new row via TouchSession (internal/monitor/session.go:236-295, called from internal/hookcmd/claude.go:161) and InitSession (session.go:104-121). The intended dedup -- collision-invalidation by process/tmux-pane (session.go:280-292) -- is INERT because TMUX_PANE is empty in the hook env: process is NULL on 728/744 rows and COALESCE(NULLIF(excluded.process,'')) refuses to store an empty pane, so there is no pane to collide on.

EVIDENCE: Pattern-A ghost rows (ended, NULL process, no session_statuses, <10s) = 298 DB-wide under the loose filter; 37 under the strict prune criteria (kind_id=1, not-newest-per-task). Examples: task 894 = three 4-9s rows; task 1362 = one real row (id 490) + five sub-10s ghosts.

DISPROVEN: bg-agent double-insert -- decorate-before-touch ordering at claude.go:154 is correct (DecorateBgSession UPDATEs by short_id before TouchSession); all sampled target rows are kind_id=1.

FIX: add an active_task_id-scoped fallback dedup for the empty-pane case so SessionStart folds into / reuses the most-recent non-ended row for the same task instead of inserting a fresh-UUID row.

Other refs: session.go:38-60 BindSessionToTask, :341-375 RecordBgAgentSession, :447-465 DecorateBgSession; claude.go:154-180 decorate->touch->init ordering, :296-298 EndSession (SessionEnd, rarely fires).