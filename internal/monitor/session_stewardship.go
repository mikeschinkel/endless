package monitor

// Focus and stewardship for the session-status board (E-2188).
//
// Two display-time annotations, layered on a row set the same way hides
// (session_hidden.go) and relations (session_relation.go) are: the row query
// stays viewer-agnostic, and what depends on WHO is looking is filled in after.
//
// FOCUS is the viewing session's sessions.focus_task_id — the task its
// conversation touched last. The renderer highlights it, and it keeps a task on
// the board even when another session is its steward. It does NOT outrank an
// explicit `session hide --task` (E-2293): focus is a pointer at what the
// conversation touched, not the task the session is on, and hiding a row in a
// session that is not its steward is the user choosing the steward.
//
// STEWARDSHIP answers "the same task is on several sessions' boards — which one
// keeps it?" (ED-1605: the steward is the session whose monitor shows the task
// so it gets spawned or rejected; the OWNER is the session that claimed it, a
// different thing). It is computed here, every time a board is drawn, and never
// written anywhere, so it corrects itself the moment a session ends. The rules:
//
//   - Only PRESENT sessions count: presentSession, the claim guard's own test
//     — a live recorded state and a liveness that is not `dead` (E-2300).
//     E-2293 had required `live` here, because a session on a tmux server that
//     no longer existed read `unknown` forever and so stewarded its filings
//     forever; now such a server is judged gone where it can be proven, and the
//     one presence test serves every reader. Idle state is irrelevant: an idle
//     session whose pane is there is present.
//   - A task's CLAIMING session is left out of the count entirely: being worked
//     by the session that claimed it is the expected case, not a duplicate, and
//     it already shows as ⟳ doing everywhere else. So is that session's focus on
//     its own claimed task.
//   - The steward is the live session that filed the task; failing
//     that, the ONLY live session that revisited (updated) it. Two or more
//     candidates on the deciding rung is AMBIGUOUS: there is no steward, and it
//     is marked on every board that shows it.
//   - An update never takes stewardship from a filer: adding a note to someone
//     else's task is ordinary and must not move it.
//   - A session that SPAWNED the task (relation spawned, E-2405) counts exactly
//     as a filer does. Spawning is the same act of putting a task in motion
//     from this board, and the spawner is the board that follows it until it
//     settles. The spawned session claims the task, so it is the OWNER and is
//     left out of the count like every claimer.
//
// A board shows a task unless another present session is its steward — and even
// then it shows a task it has in focus, or one the viewer PINNED with `session
// unhide --task` (E-2293: the user's explicit choice outranks the derivation).
// The DUPLICATE-WORK mark warns that another session may already have done real
// work on the task, and is set when the task is ambiguous, or is focused on one
// board and stewarded by another (on both of those boards). Once exactly one
// present session has CLAIMED the task the warning has done its job — the user
// knows where the work lives — so the mark is cleared; two present claimants
// are a genuine collision and always raise it (E-2293).
//
// The claimed task's own row, its parent and its spawner are never hidden and
// never marked: they are the board's frame, not entries on it.

import (
	"database/sql"

	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// taskStewardship is what the live sessions other than a task's claimer have
// recorded against it.
type taskStewardship struct {
	filers     []int64
	revisiters []int64
	focusers   []int64
	// claimers counts present sessions whose sessions.task_id is the task. It
	// is not part of the ladder — a claimer is the OWNER, not a steward — and
	// only decides the duplicate mark.
	claimers int
}

// steward applies the ladder: the single filer, else the single revisiter.
// ambiguous is true when the deciding rung has more than one session; steward is
// then 0, as it is when no live session has any claim at all.
func (o taskStewardship) steward() (steward int64, ambiguous bool) {
	for _, rung := range [][]int64{o.filers, o.revisiters} {
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

// AnnotateSessionStatusStewardship fills Focused, Pinned, DuplicateWork and
// StewardedElsewhere on each row for the board of `viewer` anchored on `focal` (0 for the no-goal
// view). The board's own sessions are the viewer plus every live session that
// claimed `focal` — the same sessions whose session_tasks rows SessionStatusRows
// draws the board from — so stewardship held by any of them is stewardship HERE.
//
// viewer == 0 (no session resolved) annotates nothing, on the rule the hide
// annotator follows: a board that cannot identify its viewer must never
// suppress rows on some other session's behalf.
func AnnotateSessionStatusStewardship(rows []SessionStatusRow, viewer, focal int64) error {
	if len(rows) == 0 || viewer == 0 {
		return nil
	}
	db, err := DB()
	if err != nil {
		return err
	}

	// RefreshLiveness, not livenessReady: `session monitor` repaints this board
	// in a loop, and the once-guard would freeze presence at the first frame.
	// Reaching no server is not an error; every session then reads `unknown`,
	// which suppresses nothing — the board over-shows rather than hides.
	if err = RefreshLiveness(); err != nil {
		return err
	}

	focus, err := sessionFocusTask(db, viewer)
	if err != nil {
		return err
	}
	pinned, err := SessionPinnedTasks(viewer)
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
		rows[i].Pinned = pinned[rows[i].ID]
		if !isBoardFrame(rows[i]) {
			candidates = append(candidates, rows[i].ID)
		}
	}
	stewarded, err := liveStewardship(db, candidates)
	if err != nil {
		return err
	}

	for i := range rows {
		r := &rows[i]
		if isBoardFrame(*r) {
			continue
		}
		o := stewarded[r.ID]
		steward, ambiguous := o.steward()
		switch {
		case ambiguous:
			r.DuplicateWork = true
		case steward == 0:
			// Nobody live has a claim on it: it belongs to whichever board
			// shows it (an epic child, a dependent, a blocker).
		case !board[steward]:
			switch {
			case r.Focused:
				r.DuplicateWork = true
			case !r.Pinned:
				r.StewardedElsewhere = true
			}
		default:
			for _, s := range o.focusers {
				if !board[s] {
					r.DuplicateWork = true
					break
				}
			}
		}
		switch {
		case o.claimers >= 2:
			r.DuplicateWork = true
		case o.claimers == 1:
			r.DuplicateWork = false
		}
	}
	return nil
}

// isBoardFrame reports whether a row is one stewardship never touches: the
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

// boardSessions is the set of sessions whose stewardship counts as this board's:
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

// liveStewardship gathers, for each task id, the filers (and, since E-2405,
// spawners), revisiters and
// focusers among present sessions — excluding each task's own claiming session,
// and excluding from filers and revisiters any session that hid the task —
// plus the number of present sessions that claimed it.
// Tasks nobody live has touched are simply absent from the map.
func liveStewardship(db *sql.DB, taskIDs []int64) (map[int64]taskStewardship, error) {
	out := make(map[int64]taskStewardship)
	ph, args := intPlaceholders(taskIDs)
	if ph == "" {
		return out, nil
	}

	rows, err := db.Query(
		`SELECT st.task_id, st.session_id, st.relation_id
		   FROM session_tasks st JOIN sessions s ON s.id = st.session_id
		   JOIN session_liveness sl ON sl.session_id = s.id
		  WHERE st.task_id IN (`+ph+`)
		    AND `+presentSession+`
		    AND s.task_id IS NOT st.task_id
		    AND st.relation_id IN (?, ?, ?)
		    AND NOT EXISTS (
		      SELECT 1 FROM session_hidden_tasks h
		       WHERE h.session_id = st.session_id AND h.task_id = st.task_id)
		  ORDER BY st.session_id`,
		append(args,
			int(sessiontaskrelation.RelationFiled),
			int(sessiontaskrelation.RelationSpawned),
			int(sessiontaskrelation.RelationRevisited))...,
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
		// E-2405: spawned lands on the filer rung, not a rung of its own —
		// "stewardship counts a spawned session as it counts a filing one".
		switch sessiontaskrelation.Relation(rel) {
		case sessiontaskrelation.RelationFiled, sessiontaskrelation.RelationSpawned:
			o.filers = append(o.filers, sessionID)
		default:
			o.revisiters = append(o.revisiters, sessionID)
		}
		out[taskID] = o
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	_, args = intPlaceholders(taskIDs)
	frows, err := db.Query(
		`SELECT s.focus_task_id, s.id FROM sessions s
		   JOIN session_liveness sl ON sl.session_id = s.id
		  WHERE s.focus_task_id IN (`+ph+`)
		    AND `+presentSession+`
		    AND s.task_id IS NOT s.focus_task_id
		  ORDER BY s.id`,
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
	if err := frows.Err(); err != nil {
		return nil, err
	}

	_, args = intPlaceholders(taskIDs)
	crows, err := db.Query(
		`SELECT s.task_id, COUNT(*) FROM sessions s
		   JOIN session_liveness sl ON sl.session_id = s.id
		  WHERE s.task_id IN (`+ph+`)
		    AND `+presentSession+`
		  GROUP BY s.task_id`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var taskID int64
		var n int
		if err := crows.Scan(&taskID, &n); err != nil {
			return nil, err
		}
		o := out[taskID]
		o.claimers = n
		out[taskID] = o
	}
	return out, crows.Err()
}
