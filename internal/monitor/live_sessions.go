package monitor

import (
	"fmt"
	"strings"
)

// LiveSession is the row shape returned by ListLiveSessions, intended for
// JSON serialization to Python callers. PaneID is set to Process when
// Process looks like a tmux pane id ("%<digits>"), else nil — so the
// Python side can distinguish tmux from non-tmux sessions without
// re-parsing the process string.
type LiveSession struct {
	SessionID        string  `json:"session_id"`
	EndlessSessionID int64   `json:"endless_session_id"`
	ProjectID        int64   `json:"project_id"`
	Platform         string  `json:"platform"`
	State            string  `json:"state"`
	ActiveTaskID     *int64  `json:"active_task_id"`
	Process          string  `json:"process"`
	PaneID           *string `json:"pane_id"`
	StartedAt        string  `json:"started_at"`
	LastActivity     string  `json:"last_activity"`
	Summary          string  `json:"summary"`
}

// ListLiveSessions returns all non-ended sessions for projectID, ordered
// by most-recent activity first. Replaces the Python-side
// `_read_live_companions` glob-and-filter pattern (E-1426).
//
// Liveness here is state != 'ended'. Crashed-Claude-but-pane-alive is
// not detected; ReapDeadTmuxPanes at SessionStart handles pane-gone
// cleanup, and TouchSession's collision invalidation handles pane reuse.
func ListLiveSessions(projectID int64) ([]LiveSession, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT session_id, id, COALESCE(project_id, 0), platform, state,
		        active_task_id, COALESCE(process, ''),
		        COALESCE(started_at, ''), COALESCE(last_activity, ''),
		        COALESCE(summary, '')
		 FROM sessions
		 WHERE state != 'ended' AND project_id = ?
		 ORDER BY last_activity DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("query live sessions: %w", err)
	}
	defer rows.Close()

	out := []LiveSession{}
	for rows.Next() {
		var s LiveSession
		if err := rows.Scan(
			&s.SessionID, &s.EndlessSessionID, &s.ProjectID, &s.Platform, &s.State,
			&s.ActiveTaskID, &s.Process, &s.StartedAt, &s.LastActivity, &s.Summary,
		); err != nil {
			return nil, fmt.Errorf("scan live session: %w", err)
		}
		if strings.HasPrefix(s.Process, "%") {
			p := s.Process
			s.PaneID = &p
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate live sessions: %w", err)
	}
	return out, nil
}
