package events

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
)

// Session-task visibility verbs (E-2293): `session hide --task` and `session
// unhide --task`, which until E-2293 wrote session_hidden_tasks straight from
// Python. They moved here, onto the same emit path as `session task remove`,
// when unhide gained a second meaning that needs a second table.
//
// A (session, task) pair is in one of three display states, and the two verbs
// move it between them:
//
//   - hidden  (session_hidden_tasks) — the user asked this session's board to
//     drop the row.
//   - pinned  (session_pinned_tasks) — the user asked this session's board to
//     KEEP the row even while another session stewards it. Stewardship is a
//     display-time derivation; a pin is the user overriding it.
//   - neither — the derivation decides.
//
// hide:   any state → hidden (a pin is cleared: the newer explicit choice wins).
// unhide: hidden → neither; neither → pinned. Unhiding a row nobody hid is the
//         user saying "show me this", and the only thing that can still be
//         withholding it is stewardship — so that is what the pin overrides.
//
// Hiding the session's OWN claimed task is refused: it is the board's anchor,
// the task the session is actually on. A hide does outrank the session's focus
// pointer (focus_task_id), which is only the task its conversation touched last.

// execSessionTasksHidden handles KindSessionTasksHidden.
func execSessionTasksHidden(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	sessionID, taskIDs, err := membershipRequest(db, evt, "session_tasks.hidden")
	if err != nil {
		return nil, err
	}
	if err := requireLiveTasks(db, taskIDs, "session_tasks.hidden"); err != nil {
		return nil, err
	}

	own, err := sessionClaimedTask(db, sessionID)
	if err != nil {
		return nil, err
	}
	for _, taskID := range taskIDs {
		if taskID == own {
			// Refused before any row is touched, and the message names the way
			// forward, so there is nothing to put to the user.
			return nil, refusal.NoReport(
				fmt.Sprintf(
					"events: session_tasks.hidden refuses E-%d: it is the task this session "+
						"claimed, the anchor of its own board", taskID,
				),
				"Drop that id from the request and retry")
		}
	}

	var hidden, already []int64
	for _, taskID := range taskIDs {
		res, err := db.Exec(
			`INSERT OR IGNORE INTO session_hidden_tasks (session_id, task_id, hidden_at)
			 VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%S', 'now'))`,
			sessionID, taskID,
		)
		if err != nil {
			return nil, fmt.Errorf("events: hide (%d, %d): %w", sessionID, taskID, err)
		}
		if _, err := db.Exec(
			`DELETE FROM session_pinned_tasks WHERE session_id = ? AND task_id = ?`,
			sessionID, taskID,
		); err != nil {
			return nil, fmt.Errorf("events: clear pin (%d, %d): %w", sessionID, taskID, err)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			hidden = append(hidden, taskID)
		} else {
			already = append(already, taskID)
		}
	}
	return &ExecuteResult{Markdown: renderHidden(sessionID, hidden, already)}, nil
}

// execSessionTasksUnhidden handles KindSessionTasksUnhidden.
func execSessionTasksUnhidden(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	sessionID, taskIDs, err := membershipRequest(db, evt, "session_tasks.unhidden")
	if err != nil {
		return nil, err
	}
	if err := requireLiveTasks(db, taskIDs, "session_tasks.unhidden"); err != nil {
		return nil, err
	}

	var unhidden, pinned, already []int64
	for _, taskID := range taskIDs {
		res, err := db.Exec(
			`DELETE FROM session_hidden_tasks WHERE session_id = ? AND task_id = ?`,
			sessionID, taskID,
		)
		if err != nil {
			return nil, fmt.Errorf("events: unhide (%d, %d): %w", sessionID, taskID, err)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			unhidden = append(unhidden, taskID)
			continue
		}
		res, err = db.Exec(
			`INSERT OR IGNORE INTO session_pinned_tasks (session_id, task_id, pinned_at)
			 VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%S', 'now'))`,
			sessionID, taskID,
		)
		if err != nil {
			return nil, fmt.Errorf("events: pin (%d, %d): %w", sessionID, taskID, err)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			pinned = append(pinned, taskID)
		} else {
			already = append(already, taskID)
		}
	}
	return &ExecuteResult{Markdown: renderUnhidden(sessionID, unhidden, pinned, already)}, nil
}

// sessionClaimedTask reads sessions.task_id, 0 when the session claimed nothing.
func sessionClaimedTask(db dbQuerier, sessionID int64) (int64, error) {
	var taskID sql.NullInt64
	err := db.QueryRow(`SELECT task_id FROM sessions WHERE id = ?`, sessionID).Scan(&taskID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("events: read claimed task of session %d: %w", sessionID, err)
	}
	return taskID.Int64, nil
}

// renderHidden formats the `session hide --task` result for chat.
func renderHidden(sessionID int64, hidden, already []int64) string {
	var b strings.Builder
	if len(hidden) > 0 {
		fmt.Fprintf(&b, "Hid %s for ES-%d.\n", taskList(sortedIDs(hidden)), sessionID)
	}
	if len(already) > 0 {
		fmt.Fprintf(&b, "%s already hidden for ES-%d.\n", taskList(sortedIDs(already)), sessionID)
	}
	return b.String()
}

// renderUnhidden formats the `session unhide --task` result for chat. A pin is
// spelled out, because it is the half of unhide a reader would not expect.
func renderUnhidden(sessionID int64, unhidden, pinned, already []int64) string {
	var b strings.Builder
	if len(unhidden) > 0 {
		fmt.Fprintf(&b, "Unhid %s for ES-%d.\n", taskList(sortedIDs(unhidden)), sessionID)
	}
	if len(pinned) > 0 {
		fmt.Fprintf(&b,
			"Pinned %s to ES-%d's board: shown there even while another session stewards it "+
				"(`session hide --task` undoes it).\n",
			taskList(sortedIDs(pinned)), sessionID)
	}
	if len(already) > 0 {
		fmt.Fprintf(&b, "%s already pinned to ES-%d's board.\n", taskList(sortedIDs(already)), sessionID)
	}
	return b.String()
}

func sortedIDs(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
