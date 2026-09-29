package monitor

// Focus and ownership for the session-status board (E-2188).
//
// Two display-time annotations, layered on a row set the same way hides
// (session_hidden.go) and relations (session_relation.go) are: the row query
// stays viewer-agnostic, and what depends on WHO is looking is filled in after.
//
// FOCUS is the viewing session's sessions.focus_task_id — the task its
// conversation touched last. The renderer highlights it, and it overrides every
// kind of suppression: a focused task is shown even when hidden, and even when
// another session owns it.
//
// OWNERSHIP answers "the same task is on several sessions' boards — which one
// keeps it?". It is computed here, every time a board is drawn, and never
// written anywhere, so it corrects itself the moment a session ends. The rules:
//
//   - Only LIVE sessions count, by the same state test the ⟳ in-flight column
//     uses. A dead session neither owns a task nor takes one off a live board.
//   - A task's CLAIMING session is left out of the count entirely: being worked
//     by the session that claimed it is the expected case, not a duplicate, and
//     it already shows as ⟳ doing everywhere else. So is that session's focus on
//     its own claimed task.
//   - The owner is the live session that surfaced (filed) the task; failing
//     that, the ONLY live session that revisited (updated) it. Two or more
//     candidates on the deciding rung is AMBIGUOUS: nobody owns it, and it is
//     marked on every board that shows it.
//   - An update never takes ownership from a filer: adding a note to someone
//     else's task is ordinary and must not move it.
//
// A board shows a task unless another live session owns it — and even then it
// shows a task it has in focus. The DUPLICATE-WORK mark warns that another
// session may already have done real work on the task, and is set when the
// task is ambiguous, or is focused on one board and owned by another (on both
// of those boards).
//
// The claimed task's own row, its parent and its spawner are never hidden and
// never marked: they are the board's frame, not entries on it.

import (
	"database/sql"

	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// taskOwnership is what the live sessions other than a task's claimer have
// recorded against it.
type taskOwnership struct {
	surfacers  []int64
	revisiters []int64
	focusers   []int64
}

// owner applies the ladder: the single surfacer, else the single revisiter.
// ambiguous is true when the deciding rung has more than one session; owner is
// then 0, as it is when no live session has any claim at all.
func (o taskOwnership) owner() (owner int64, ambiguous bool) {
	for _, rung := range [][]int64{o.surfacers, o.revisiters} {
		switch len(rung) {
		case 0:
			continue
		case 1:
			return rung[0], false
		default:
			return 0, true
		}
	}
	return 0, false
}

// AnnotateSessionStatusOwnership fills Focused, DuplicateWork and OwnedElsewhere
// on each row for the board of `viewer` anchored on `focal` (0 for the no-goal
// view). The board's own sessions are the viewer plus every live session that
// claimed `focal` — the same sessions whose session_tasks rows SessionStatusRows
// draws the board from — so ownership held by any of them is ownership HERE.
//
// viewer == 0 (no session resolved) annotates nothing, on the rule the hide
// annotator follows: a board that cannot identify its viewer must never
// suppress rows on some other session's behalf.
func AnnotateSessionStatusOwnership(rows []SessionStatusRow, viewer, focal int64) error {
	if len(rows) == 0 || viewer == 0 {
		return nil
	}
	db, err := DB()
	if err != nil {
		return err
	}

	focus, err := sessionFocusTask(db, viewer)
	if err != nil {
		return err
	}
	board, err := boardSessions(db, viewer, focal)
	if err != nil {
		return err
	}

	var candidates []int64
	for i := range rows {
		rows[i].Focused = focus != 0 && rows[i].ID == focus
		if !isBoardFrame(rows[i]) {
			candidates = append(candidates, rows[i].ID)
		}
	}
	owned, err := liveOwnership(db, candidates)
	if err != nil {
		return err
	}

	for i := range rows {
		r := &rows[i]
		if isBoardFrame(*r) {
			continue
		}
		o := owned[r.ID]
		owner, ambiguous := o.owner()
		switch {
		case ambiguous:
			r.DuplicateWork = true
		case owner == 0:
			// Nobody live has a claim on it: it belongs to whichever board
			// surfaced it (an epic child, a dependent, a blocker).
		case !board[owner]:
			if r.Focused {
				r.DuplicateWork = true
			} else {
				r.OwnedElsewhere = true
			}
		default:
			for _, s := range o.focusers {
				if !board[s] {
					r.DuplicateWork = true
					break
				}
			}
		}
	}
	return nil
}

// isBoardFrame reports whether a row is one ownership never touches: the
// board's claimed task, its parent, and its spawner.
func isBoardFrame(r SessionStatusRow) bool {
	return r.IsFocal || r.IsParent || r.IsFrom
}

// sessionFocusTask reads one session's focus_task_id, 0 when unset.
func sessionFocusTask(db *sql.DB, sessionID int64) (int64, error) {
	var focus sql.NullInt64
	err := db.QueryRow(
		`SELECT focus_task_id FROM sessions WHERE id = ?`, sessionID,
	).Scan(&focus)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return focus.Int64, nil
}

// boardSessions is the set of sessions whose ownership counts as this board's:
// the viewer, plus every live session that claimed the focal task.
func boardSessions(db *sql.DB, viewer, focal int64) (map[int64]bool, error) {
	board := map[int64]bool{viewer: true}
	if focal == 0 {
		return board, nil
	}
	rows, err := db.Query(
		`SELECT id FROM sessions WHERE task_id = ? AND state IN (`+liveSessionStates+`)`,
		focal,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		board[id] = true
	}
	return board, rows.Err()
}

// liveOwnership gathers, for each task id, the surfacers, revisiters and
// focusers among live sessions — excluding each task's own claiming session.
// Tasks nobody live has touched are simply absent from the map.
func liveOwnership(db *sql.DB, taskIDs []int64) (map[int64]taskOwnership, error) {
	out := make(map[int64]taskOwnership)
	ph, args := intPlaceholders(taskIDs)
	if ph == "" {
		return out, nil
	}

	rows, err := db.Query(
		`SELECT st.task_id, st.session_id, st.relation_id
		   FROM session_tasks st JOIN sessions s ON s.id = st.session_id
		  WHERE st.task_id IN (`+ph+`)
		    AND s.state IN (`+liveSessionStates+`)
		    AND s.task_id IS NOT st.task_id
		    AND st.relation_id IN (?, ?)
		  ORDER BY st.session_id`,
		append(args, int(sessiontaskrelation.RelationSurfaced), int(sessiontaskrelation.RelationRevisited))...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, sessionID, rel int64
		if err := rows.Scan(&taskID, &sessionID, &rel); err != nil {
			return nil, err
		}
		o := out[taskID]
		if sessiontaskrelation.Relation(rel) == sessiontaskrelation.RelationSurfaced {
			o.surfacers = append(o.surfacers, sessionID)
		} else {
			o.revisiters = append(o.revisiters, sessionID)
		}
		out[taskID] = o
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	_, args = intPlaceholders(taskIDs)
	frows, err := db.Query(
		`SELECT focus_task_id, id FROM sessions
		  WHERE focus_task_id IN (`+ph+`)
		    AND state IN (`+liveSessionStates+`)
		    AND task_id IS NOT focus_task_id
		  ORDER BY id`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		var taskID, sessionID int64
		if err := frows.Scan(&taskID, &sessionID); err != nil {
			return nil, err
		}
		o := out[taskID]
		o.focusers = append(o.focusers, sessionID)
		out[taskID] = o
	}
	return out, frows.Err()
}
