package monitor

import (
	"database/sql"
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// SessionStatusRow is one task row in the per-session "what's next" view
// (E-1465). It carries the raw fields the renderer needs; all icon/letter/
// phase-char/sort derivation happens in the caller (internal/sessionstatuscmd)
// so the rendering rules stay testable without a DB.
//
// IsFocal/IsParent/IsFrom/InFlight are mutually-prioritized decorations computed
// in-query: IsFocal is the window's own active task; IsParent the focal's real
// task-tree parent (tasks.parent_id); IsFrom the SPAWNING session's active task
// (session lineage — "where this session came from", NOT a tree relation);
// InFlight any OTHER live session's active task. IsParent and IsFrom are distinct
// (E-1694): the spawner is rarely the task-tree parent, and conflating them was
// E-1465's original mislabel. BlockedByN counts open tasks that block this one;
// BlocksN counts tasks this one blocks (regardless of their status — it drives
// the ⏸ "blocks" marker).
type SessionStatusRow struct {
	ID         int64
	Title      string
	Status     string
	Phase      string
	TypeSlug   string
	HasText    bool
	IsFocal    bool
	IsParent   bool
	IsFrom     bool
	InFlight   bool
	BlockedByN int
	BlocksN    int
}

// terminalStatusSet is the canonical "done-work" status set: these unblock
// dependents (see `endless guide tasks`) and are omitted from the session-status
// view unless they are the focal/parent row or --all is passed. Kept in sync
// with the prototype spec (~/.config/endless/session-status.sql).
const terminalStatusSet = "'confirmed','assumed','declined','obsolete','completed'"

// ResolveSessionStatusFocal resolves the focal task for the session-status view,
// matching claude.go's documented priority (session active task >
// @endless_task_id) with a global most-recent last resort (E-1465 / ED-1523):
//
//  1. GetActiveTaskForPane(tmuxPane) — the live session's active_task via
//     process match (state != 'ended'), pane then window-scoped.
//  2. The window's @endless_task_id tmux option — the COMMON fallback when (1)
//     is empty (active windows are often needs_input with NULL active_task, and
//     bound sessions can be ended with NULL process).
//  3. The most-recently-active live session's active_task_id, machine-wide.
//
// Returns 0 (no error) when nothing resolves — the caller renders an empty view.
func ResolveSessionStatusFocal(tmuxPane string) (int64, error) {
	if info, err := GetActiveTaskForPane(tmuxPane); err == nil {
		return info.TaskID, nil
	} else if !errors.Is(err, ErrNoActiveTask) {
		return 0, err
	}

	if v := tmuxWindowOption(tmuxPane, "@endless_task_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			return id, nil
		}
	}

	db, err := DB()
	if err != nil {
		return 0, err
	}
	var id int64
	err = db.QueryRow(
		`SELECT active_task_id FROM sessions
		  WHERE state != 'ended' AND active_task_id IS NOT NULL
		  ORDER BY last_activity DESC LIMIT 1`,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ResolveSessionStatusParentSession reads the window's @endless_spawned_by marker
// and returns the spawning session's integer sessions.id, or 0 when the window
// was not spawned by `endless task spawn` or the marker is a `pid-<n>` fallback
// (a non-Claude spawner with no session row). The marker holds a session id,
// NOT a pane id — the bash prototype's pane-based lookup is wrong; the Go
// command resolves the parent's active task directly from this id in the query.
func ResolveSessionStatusParentSession(tmuxPane string) int64 {
	v := tmuxWindowOption(tmuxPane, "@endless_spawned_by")
	if v == "" {
		return 0
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// tmuxWindowOption reads a tmux window option (e.g. "@endless_task_id") for the
// given pane's window. Returns "" when not in tmux, the option is unset, or tmux
// errors. Mirrors hookcmd.tmuxTaskID/tmuxSpawnedBy, reused here so the read
// command composes the same resolution rather than re-deriving pane→session.
func tmuxWindowOption(pane, name string) string {
	if pane == "" {
		return ""
	}
	out, err := exec.Command(
		"tmux", "display-message", "-p", "-t", pane, "#{"+name+"}",
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SessionStatusRows returns the task rows for the session-status view of focal task
// `focal`, with `parentSession` the spawning session's id (0 if none). The row
// set, per the prototype spec (~/.config/endless/session-status.sql) plus E-1685
// and E-1691:
//
//   - every task touched (via session_tasks) by ANY live-or-dead session whose
//     active_task_id = focal (cross-project; robust to duplicate session rows),
//   - ∪ the focal task itself,
//   - ∪ the focal's real task-tree parent (tasks.parent_id) — the ↑ parent row,
//   - ∪ the spawning session's active task — the ↩ from row (session lineage),
//   - ∪ the focal task's DIRECT dependents — tasks T it blocks
//     (task_deps source=focal, target=T, dep_type='blocks'). These are computed
//     at read time, NOT written into session_tasks: that table is a projection
//     of the event ledger, and a dependent has no backing task.* event (E-1685).
//     A dependent carries ⊗ while the focal is open (its BlockedByN counts the
//     open focal); when the focal lands and the block clears, BlockedByN drops to
//     0 and ⊗ disappears with no special highlight. One hop only, not the
//     transitive closure.
//   - ∪ the focal task's DIRECT children — tasks T with parent_id = focal
//     (E-1691). For an epic the children ARE the work; surfacing them lets the
//     session's pane carry the subtasks. Read-time only, same invariant reason
//     as the dependents. One level only — working a child surfaces ITS children
//     in that child's own session.
//
// Done-work (terminal status) is omitted UNLESS the row is the focal, parent, or
// from (spawner) row, or includeAll is true. Returns an empty slice when focal
// is 0.
func SessionStatusRows(focal, parentSession int64, includeAll bool) ([]SessionStatusRow, error) {
	if focal == 0 {
		return nil, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}

	allFlag := 0
	if includeAll {
		allFlag = 1
	}

	q := `
WITH
ftask(tid) AS (SELECT ?),
-- sfoc.stid = the SPAWNING session's active task (session lineage → ↩ from).
sfoc(stid) AS (SELECT active_task_id FROM sessions WHERE id = ?),
-- rpar.rpid = the focal's real task-tree parent (tasks.parent_id → ↑ parent).
rpar(rpid) AS (SELECT parent_id FROM tasks WHERE id = (SELECT tid FROM ftask)),
base AS (
  SELECT t.id, t.title, t.status, t.phase, t.text, t.type_id
    FROM session_tasks st JOIN tasks t ON t.id = st.task_id
   WHERE st.session_id IN (
     SELECT id FROM sessions WHERE active_task_id = (SELECT tid FROM ftask)
   )
  UNION
  SELECT t.id, t.title, t.status, t.phase, t.text, t.type_id
    FROM tasks t WHERE t.id = (SELECT tid FROM ftask)
  UNION
  SELECT t.id, t.title, t.status, t.phase, t.text, t.type_id
    FROM tasks t, rpar WHERE t.id = rpar.rpid
  UNION
  SELECT t.id, t.title, t.status, t.phase, t.text, t.type_id
    FROM tasks t, sfoc WHERE t.id = sfoc.stid
  UNION
  -- E-1685: the focal task's direct dependents (tasks it blocks), read-time
  -- only. Computed here rather than materialized into session_tasks so the
  -- projection-of-the-event-ledger invariant holds. The terminal-status filter
  -- in the final SELECT drops done dependents unless --all; the BlockedByN
  -- column drives their ⊗ while the focal stays open.
  SELECT t.id, t.title, t.status, t.phase, t.text, t.type_id
    FROM tasks t
   WHERE EXISTS (
     SELECT 1 FROM task_deps d
      WHERE d.source_type = 'task' AND d.target_type = 'task'
        AND d.dep_type = 'blocks'
        AND d.source_id = (SELECT tid FROM ftask)
        AND d.target_id = t.id
   )
  UNION
  -- E-1691: the focal task's DIRECT children. For an epic the children ARE the
  -- work, yet the row set above omits them. Computed at read time (not written
  -- into session_tasks) for the same projection-invariant reason as E-1685's
  -- dependents. Direct (one level) only: working a child surfaces ITS children
  -- in that child's own session, keeping each view one level deep rather than
  -- exploding the whole subtree. The terminal-status filter in the final SELECT
  -- drops done children unless --all, matching the dependent behavior.
  SELECT t.id, t.title, t.status, t.phase, t.text, t.type_id
    FROM tasks t WHERE t.parent_id = (SELECT tid FROM ftask)
),
enr AS (
  SELECT b.id, b.title, b.status, b.phase,
    COALESCE((SELECT slug FROM task_types WHERE id = b.type_id), '') AS type_slug,
    (b.text IS NOT NULL AND b.text <> '') AS has_text,
    (b.id = (SELECT tid FROM ftask)) AS is_focal,
    -- rpar.rpid is NULL when the focal has no parent; COALESCE keeps is_parent a
    -- real boolean rather than NULL. The focal-self guard avoids self-marking.
    COALESCE(b.id = (SELECT rpid FROM rpar), 0) AND b.id <> (SELECT tid FROM ftask) AS is_parent,
    -- sfoc.stid is NULL when there is no spawning session (or it has no active
    -- task). is_from yields to is_parent in the renderer when both are true.
    COALESCE(b.id = (SELECT stid FROM sfoc), 0) AND b.id <> (SELECT tid FROM ftask) AS is_from,
    (EXISTS(
       SELECT 1 FROM sessions s
        WHERE s.state != 'ended' AND s.active_task_id = b.id
     ) AND b.id <> (SELECT tid FROM ftask)) AS in_flight,
    (SELECT count(*) FROM task_deps d JOIN tasks blk ON blk.id = d.source_id
       WHERE d.source_type = 'task' AND d.target_type = 'task'
         AND d.dep_type = 'blocks' AND d.target_id = b.id
         AND blk.status NOT IN (` + terminalStatusSet + `)) AS blocked_by_n,
    (SELECT count(*) FROM task_deps d
       WHERE d.source_type = 'task' AND d.source_id = b.id
         AND d.dep_type = 'blocks') AS blocks_n
  FROM base b
)
SELECT id, title, status, phase, type_slug, has_text,
       is_focal, is_parent, is_from, in_flight, blocked_by_n, blocks_n
  FROM enr
 WHERE (? = 1) OR is_focal OR is_parent OR is_from
       OR status NOT IN (` + terminalStatusSet + `)
`

	rows, err := db.Query(q, focal, parentSession, allFlag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SessionStatusRow
	for rows.Next() {
		var r SessionStatusRow
		if err := rows.Scan(
			&r.ID, &r.Title, &r.Status, &r.Phase, &r.TypeSlug, &r.HasText,
			&r.IsFocal, &r.IsParent, &r.IsFrom, &r.InFlight, &r.BlockedByN, &r.BlocksN,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// intPlaceholders renders "?,?,…" with len(ids) slots and the matching []any
// args, for a dynamic SQL `IN` clause. Returns ("", nil) for an empty set so
// callers can short-circuit (an empty `IN ()` is a SQL error).
func intPlaceholders(ids []int64) (string, []any) {
	if len(ids) == 0 {
		return "", nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	return strings.Join(ph, ","), args
}

// SessionStatusBlockerEdges returns the in-set blocked-by edges among `ids`: for
// each task that is blocked, the list of its blockers that are ALSO in `ids` and
// still OPEN (blocker status not terminal). This is the blocks-DAG restricted to
// the candidate set, which `session status --tree` topologically layers into
// implementation order. Result maps target (blocked task) → []source (blockers).
// Tasks with no in-set open blocker are absent from the map (they are roots).
func SessionStatusBlockerEdges(ids []int64) (map[int64][]int64, error) {
	if len(ids) == 0 {
		return map[int64][]int64{}, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}
	ph, args := intPlaceholders(ids)
	// Both endpoints must be in the candidate set; the blocker must be open.
	q := `
SELECT d.target_id, d.source_id
  FROM task_deps d JOIN tasks blk ON blk.id = d.source_id
 WHERE d.source_type = 'task' AND d.target_type = 'task'
   AND d.dep_type = 'blocks'
   AND blk.status NOT IN (` + terminalStatusSet + `)
   AND d.target_id IN (` + ph + `)
   AND d.source_id IN (` + ph + `)`
	rows, err := db.Query(q, append(append([]any{}, args...), args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	edges := make(map[int64][]int64)
	for rows.Next() {
		var target, source int64
		if err := rows.Scan(&target, &source); err != nil {
			return nil, err
		}
		edges[target] = append(edges[target], source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return edges, nil
}

// SessionStatusDoOrder returns the per-session implementation order (E-1683's
// session_tasks.do_order) for the candidate `ids`, scoped to sessions whose
// active_task_id = focal — the same union scope SessionStatusRows uses. Only
// non-null do_order rows are returned; a task absent from the map has no
// explicit order. When non-empty, this OVERRIDES the DAG-derived order in
// `session status --tree`.
func SessionStatusDoOrder(focal int64, ids []int64) (map[int64]int64, error) {
	if focal == 0 || len(ids) == 0 {
		return map[int64]int64{}, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}
	ph, args := intPlaceholders(ids)
	q := `
SELECT st.task_id, st.do_order
  FROM session_tasks st JOIN sessions s ON s.id = st.session_id
 WHERE s.active_task_id = ?
   AND st.do_order IS NOT NULL
   AND st.task_id IN (` + ph + `)`
	rows, err := db.Query(q, append([]any{focal}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	order := make(map[int64]int64)
	for rows.Next() {
		var taskID, doOrder int64
		if err := rows.Scan(&taskID, &doOrder); err != nil {
			return nil, err
		}
		order[taskID] = doOrder
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return order, nil
}
