package monitor

// Per-session task hiding (E-1914). A session may suppress individual task rows
// from ITS OWN `session status` / `session monitor` listing without affecting any
// other session's view, `task next`, blocking relations, or landing. Hiding is
// display-scoped and belongs to the (session, task) pair — never to the task
// alone — which is why the state lives in its own session_hidden_tasks table
// keyed on both ids (ED-1545; see internal/schema/schema.sql for why it is not a
// column on session_tasks).
//
// A hide NEVER expires on its own. No status transition clears it, `unverified`
// included; only an explicit `session unhide --task` reverses it. When a task
// reaches a terminal status it drops off the listing anyway and its hide row goes
// inert — deliberately left in place rather than GC'd, so re-opening the task
// (revisit) restores the suppression the session asked for.

import (
	"time"
)

// SessionHiddenTasks returns the tasks session `sessionID` has hidden, as
// task_id → hidden_at. Empty (never nil) for session 0 or a session with no
// hides, so callers can index it unconditionally.
func SessionHiddenTasks(sessionID int64) (map[int64]string, error) {
	hidden := make(map[int64]string)
	if sessionID == 0 {
		return hidden, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT task_id, hidden_at FROM session_hidden_tasks WHERE session_id = ?`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID int64
		var hiddenAt string
		if err := rows.Scan(&taskID, &hiddenAt); err != nil {
			return nil, err
		}
		hidden[taskID] = hiddenAt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return hidden, nil
}

// AnnotateSessionStatusHidden fills each row's Hidden/HiddenAt from the VIEWING
// session's hides, in place. It mirrors AnnotateSessionStatusUnsettled: the row
// query is viewer-agnostic (a focal task's rows are the same whoever is looking),
// and the viewer-specific decoration is layered on afterwards, so no existing
// query signature changes.
//
// viewer == 0 (no session resolved — e.g. outside tmux) leaves every row
// unhidden, which is the right failure mode: a listing that cannot identify its
// viewer must show everything rather than silently suppress rows on some other
// session's behalf.
func AnnotateSessionStatusHidden(rows []SessionStatusRow, viewer int64) error {
	if len(rows) == 0 || viewer == 0 {
		return nil
	}
	hidden, err := SessionHiddenTasks(viewer)
	if err != nil {
		return err
	}
	for i := range rows {
		if at, ok := hidden[rows[i].ID]; ok {
			rows[i].Hidden = true
			rows[i].HiddenAt = at
		}
	}
	return nil
}

// HideSessionTasks marks each task hidden for `sessionID` and returns how many
// were newly hidden. Hiding an already-hidden task is a NO-OP, not an error (the
// existing hidden_at is preserved so `--only-hidden` keeps ordering by the
// original suppression time), so the count may be less than len(taskIDs).
func HideSessionTasks(sessionID int64, taskIDs []int64) (int, error) {
	if sessionID == 0 || len(taskIDs) == 0 {
		return 0, nil
	}
	db, err := DB()
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	added := 0
	for _, taskID := range taskIDs {
		res, err := db.Exec(
			`INSERT OR IGNORE INTO session_hidden_tasks (session_id, task_id, hidden_at)
			 VALUES (?, ?, ?)`,
			sessionID, taskID, now,
		)
		if err != nil {
			return added, err
		}
		if n, err := res.RowsAffected(); err == nil {
			added += int(n)
		}
	}
	return added, nil
}

// UnhideSessionTasks clears the hide for each task and returns how many rows were
// actually removed. Unhiding a task that is not hidden is a no-op, not an error.
func UnhideSessionTasks(sessionID int64, taskIDs []int64) (int, error) {
	if sessionID == 0 || len(taskIDs) == 0 {
		return 0, nil
	}
	db, err := DB()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, taskID := range taskIDs {
		res, err := db.Exec(
			`DELETE FROM session_hidden_tasks WHERE session_id = ? AND task_id = ?`,
			sessionID, taskID,
		)
		if err != nil {
			return removed, err
		}
		if n, err := res.RowsAffected(); err == nil {
			removed += int(n)
		}
	}
	return removed, nil
}
