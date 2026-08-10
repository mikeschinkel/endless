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
	// Liveness is the observed run state (E-1898): "live" (pane present on a
	// server we reached), "unknown" (server unreachable — no opinion), or
	// "unbound" (no pane binding at all, e.g. a background agent). "dead" never
	// appears: ListLiveSessions filters those out. Exposed so a caller can tell
	// a proven-live owner from one we merely could not disprove, rather than
	// inferring it from the row's presence.
	Liveness string `json:"liveness"`
}

// ListLiveSessions returns the non-ended sessions for projectID that are not
// OBSERVABLY gone, ordered by most-recent activity first. Replaces the
// Python-side `_read_live_companions` glob-and-filter pattern (E-1426).
//
// Two filters, and the difference between them is the point (E-1898):
//
//   - state != 'ended' — a recorded FACT. Something reported the session over.
//   - liveness != 'dead' — a fresh OBSERVATION. We reached the session's tmux
//     server and its pane was not there.
//
// This is where the dead-pane reaper's job went. The spawn/claim ownership
// guard used to call a reaper first so a ghost owner would be WRITTEN to
// 'ended' before it read ownership; now the ghost simply does not appear in
// this list, and nothing was written to make that true.
//
// Sessions whose server could not be reached read 'unknown' and DELIBERATELY
// remain in the list. They are still owners as far as anyone can prove, so the
// guard keeps refusing. A wrong refusal costs a retry; the inverse hands a live
// worktree to a second session.
func ListLiveSessions(projectID int64) ([]LiveSession, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	if err = livenessReady(); err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT s.session_id, s.id, COALESCE(s.project_id, 0), s.platform, s.state,
		        s.active_task_id, COALESCE(p.address, ''),
		        COALESCE(s.started_at, ''), COALESCE(s.last_activity, ''),
		        COALESCE(s.summary, ''), sl.liveness
		 FROM sessions s
		 LEFT JOIN processes p ON p.id = s.process_id
		 JOIN session_liveness sl ON sl.session_id = s.id
		 WHERE s.state != 'ended' AND s.project_id = ?
		   AND sl.liveness != 'dead'
		 ORDER BY s.last_activity DESC`,
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
			&s.Liveness,
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
