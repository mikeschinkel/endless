package monitor

import (
	"database/sql"
	"errors"

	"github.com/mikeschinkel/endless/internal/sessionkind"
)

// FocusedBgAgent returns the background-agent session that is "focused" from
// the perspective of the coordinator session identified by coordinatorSessionID
// (the integer sessions.id). Per E-1552, focus is derived, not stored: the
// focused bg agent is the live `kind='background'` session whose task_id
// matches the child the coordinator is currently viewing
// (coordinator.task_id).
//
// Returns (nil, nil) when the coordinator has no active task or no matching bg
// agent exists — both are ordinary states, not errors. A non-nil error is
// reserved for real DB failures. When several bg agents match (shouldn't happen
// in practice), the most recently active one wins.
func FocusedBgAgent(coordinatorSessionID int64) (*SessionInfo, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	var s SessionInfo
	var kindID int64
	err = db.QueryRow(
		`SELECT bg.id, bg.session_id, COALESCE(bg.project_id,0),
		        bg.task_id, bg.epic_id, bg.kind_id,
		        bg.state, COALESCE(bg.last_activity,''), COALESCE(bg.started_at,'')
		   FROM sessions coord
		   JOIN sessions bg ON bg.task_id = coord.task_id
		  WHERE coord.id = ?
		    AND coord.task_id IS NOT NULL
		    AND bg.kind_id = ?
		    AND bg.state != 'ended'
		  ORDER BY bg.last_activity DESC
		  LIMIT 1`,
		coordinatorSessionID, int64(sessionkind.SessionKindBackground),
	).Scan(
		&s.ID, &s.SessionID, &s.ProjectID,
		&s.TaskID, &s.EpicID, &kindID,
		&s.State, &s.LastActivity, &s.StartedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.Kind = sessionkind.SessionKind(kindID)
	return &s, nil
}
