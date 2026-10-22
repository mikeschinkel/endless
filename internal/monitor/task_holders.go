package monitor

import "fmt"

// TaskHolder is a session other than the asking one whose sessions.task_id is
// the task — a session that holds it, or held it and has ended (E-2418).
// sessions.task_id is write-once (ED-1560), so it is the durable record of every
// session that ever held a task.
type TaskHolder struct {
	ID int64
	// Present is the claim guard's own test, presentSession: recorded live and
	// not observably gone. `unknown` counts — unprovable is not gone.
	Present bool
	// SamePane is true when the holder is bound to the very pane (the same
	// processes row: server and address) the asking session is. Two sessions in
	// one pane are one conversation line — a /clear, a resume, a restart of
	// `claude` where the last one exited — not two sessions competing for a task.
	SamePane bool
	// Paneless is true when the holder has no pane binding at all (liveness
	// `unbound`): it can be shown neither gone nor in some other pane.
	Paneless bool
	// Pane is the holder's pane address when it is live, "" otherwise. A pane id
	// is unique per server only, so a binding we did not see live is not printed.
	Pane string
}

// OtherTaskHolders returns every session other than sessionGUID that holds or
// held taskID, most recently active first — the order monitor.resumeByTask uses,
// so the first is the session `session goto E-N --resume` lands in.
func OtherTaskHolders(taskID int64, sessionGUID string) (holders []TaskHolder, err error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	if err = livenessReady(); err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT s.id,
		        `+presentSession+`,
		        s.process_id IS NOT NULL AND s.process_id IS (
		          SELECT process_id FROM sessions WHERE session_id = ?),
		        sl.liveness = 'unbound',
		        CASE WHEN sl.liveness = 'live' THEN COALESCE(sl.address, '') ELSE '' END
		   FROM sessions s JOIN session_liveness sl ON sl.session_id = s.id
		  WHERE s.task_id = ? AND s.session_id != ?
		  ORDER BY s.last_activity DESC, s.id DESC`,
		sessionGUID, taskID, sessionGUID,
	)
	if err != nil {
		return nil, fmt.Errorf("task holders: E-%d: %w", taskID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var h TaskHolder
		if err = rows.Scan(&h.ID, &h.Present, &h.SamePane, &h.Paneless, &h.Pane); err != nil {
			return nil, fmt.Errorf("task holders: E-%d: %w", taskID, err)
		}
		holders = append(holders, h)
	}
	return holders, rows.Err()
}
