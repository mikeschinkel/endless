package monitor

import (
	"fmt"
	"strings"

	"github.com/mikeschinkel/endless/internal/processkind"
)

// PaneSession is a live session bound to a pane on one tmux server.
type PaneSession struct {
	EndlessSessionID int64
	TaskID           int64 // 0 when the session has no task
	State            string
	Pane             string // the pane's tmux id, e.g. "%41"
}

// SessionsOnPanes returns the live sessions bound to any of panes on the tmux
// server identified by serverUUID, for `endless stop tasks` (E-2385), which
// must know which of a server's panes hold Endless sessions and which of those
// are working before it closes them.
//
// Server-scoped for GetLiveSessionByProcess's reason: a pane id is unique only
// per server, so a session bound to the same "%N" on a server that has since
// died must not be read as this server's. An empty serverUUID — a server with
// no @server_uuid — has no bindings anyone could prove, and returns none.
func SessionsOnPanes(serverUUID string, panes []string) ([]PaneSession, error) {
	if serverUUID == "" || len(panes) == 0 {
		return nil, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(panes)), ",")
	args := make([]any, 0, len(panes)+2)
	args = append(args, int(processkind.ProcessKindTmux), serverUUID)
	for _, p := range panes {
		args = append(args, p)
	}
	rows, err := db.Query(
		`SELECT s.id, COALESCE(s.task_id, 0), s.state, p.address
		 FROM sessions s
		 JOIN processes p ON p.id = s.process_id
		 WHERE p.kind_id = ? AND p.server_uuid = ? AND p.address IN (`+placeholders+`)
		   AND s.state IN (`+liveSessionStates+`)
		 ORDER BY s.id`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("sessions on panes: %w", err)
	}
	defer rows.Close()

	var out []PaneSession
	for rows.Next() {
		var s PaneSession
		if err = rows.Scan(&s.EndlessSessionID, &s.TaskID, &s.State, &s.Pane); err != nil {
			return nil, fmt.Errorf("sessions on panes: scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
