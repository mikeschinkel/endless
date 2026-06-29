package monitor

import (
	"database/sql"
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// SessionNextRow is one task row in the per-session "what's next" view
// (E-1465). It carries the raw fields the renderer needs; all icon/letter/
// phase-char/sort derivation happens in the caller (internal/sessionnextcmd)
// so the rendering rules stay testable without a DB.
//
// IsFocal/IsParent/InFlight are mutually-prioritized decorations computed
// in-query: IsFocal is the window's own active task, IsParent the spawning
// session's active task, InFlight any OTHER live session's active task.
// BlockedByN counts open tasks that block this one; BlocksN counts tasks this
// one blocks (regardless of their status — it drives the ⏸ "blocks" marker).
type SessionNextRow struct {
	ID         int64
	Title      string
	Status     string
	Phase      string
	TypeSlug   string
	HasText    bool
	IsFocal    bool
	IsParent   bool
	InFlight   bool
	BlockedByN int
	BlocksN    int
}

// terminalStatusSet is the canonical "done-work" status set: these unblock
// dependents (see `endless guide tasks`) and are omitted from the session-next
// view unless they are the focal/parent row or --all is passed. Kept in sync
// with the prototype spec (~/.config/endless/session-next.sql).
const terminalStatusSet = "'confirmed','assumed','declined','obsolete','completed'"

// ResolveSessionNextFocal resolves the focal task for the session-next view,
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
func ResolveSessionNextFocal(tmuxPane string) (int64, error) {
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

// ResolveSessionNextParentSession reads the window's @endless_spawned_by marker
// and returns the spawning session's integer sessions.id, or 0 when the window
// was not spawned by `endless task spawn` or the marker is a `pid-<n>` fallback
// (a non-Claude spawner with no session row). The marker holds a session id,
// NOT a pane id — the bash prototype's pane-based lookup is wrong; the Go
// command resolves the parent's active task directly from this id in the query.
func ResolveSessionNextParentSession(tmuxPane string) int64 {
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

// SessionNextRows returns the task rows for the session-next view of focal task
// `focal`, with `parentSession` the spawning session's id (0 if none). The row
// set, per the prototype spec (~/.config/endless/session-next.sql):
//
//   - every task touched (via session_tasks) by ANY live-or-dead session whose
//     active_task_id = focal (cross-project; robust to duplicate session rows),
//   - ∪ the focal task itself,
//   - ∪ the parent session's active task.
//
// Done-work (terminal status) is omitted UNLESS the row is the focal or parent
// row, or includeAll is true. Returns an empty slice when focal is 0.
func SessionNextRows(focal, parentSession int64, includeAll bool) ([]SessionNextRow, error) {
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
pfoc(ptid) AS (SELECT active_task_id FROM sessions WHERE id = ?),
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
    FROM tasks t, pfoc WHERE t.id = pfoc.ptid
),
enr AS (
  SELECT b.id, b.title, b.status, b.phase,
    COALESCE((SELECT slug FROM task_types WHERE id = b.type_id), '') AS type_slug,
    (b.text IS NOT NULL AND b.text <> '') AS has_text,
    (b.id = (SELECT tid FROM ftask)) AS is_focal,
    -- pfoc.ptid is NULL when there is no parent session (or it has no active
    -- task); COALESCE keeps is_parent a real boolean rather than NULL.
    COALESCE(b.id = (SELECT ptid FROM pfoc), 0) AND b.id <> (SELECT tid FROM ftask) AS is_parent,
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
       is_focal, is_parent, in_flight, blocked_by_n, blocks_n
  FROM enr
 WHERE (? = 1) OR is_focal OR is_parent
       OR status NOT IN (` + terminalStatusSet + `)
`

	rows, err := db.Query(q, focal, parentSession, allFlag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SessionNextRow
	for rows.Next() {
		var r SessionNextRow
		if err := rows.Scan(
			&r.ID, &r.Title, &r.Status, &r.Phase, &r.TypeSlug, &r.HasText,
			&r.IsFocal, &r.IsParent, &r.InFlight, &r.BlockedByN, &r.BlocksN,
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
