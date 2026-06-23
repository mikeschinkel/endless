package monitor

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mikeschinkel/endless/internal/sessionkind"
)

// BgAgent is one row in the `endless agents` listing (E-1621): a working
// background-agent session, intended for JSON serialization to the Python CLI.
// TaskID is the agent's active_task_id (the task it was dispatched on); Title is
// that task's title (empty when the task row is gone). ShortID is the dispatch
// handle from `claude --bg` (empty until/unless set).
type BgAgent struct {
	ID        int64  `json:"id"`
	ShortID   string `json:"short_id"`
	TaskID    *int64 `json:"task_id"`
	Title     string `json:"title"`
	StartedAt string `json:"started_at"`
}

// bgAgentQuery is the shared SELECT for the two listings below; the caller
// appends the scope predicate (epic vs project) and its bind value. The kind
// filter uses the typed sessionkind constant (not a hardcoded 2), matching
// CountActiveBgAgents, so it stays stable against any seed-id change.
const bgAgentQuery = `
	SELECT s.id, COALESCE(s.short_id, ''), s.active_task_id,
	       COALESCE(t.title, ''), COALESCE(s.started_at, '')
	  FROM sessions s
	  LEFT JOIN tasks t ON t.id = s.active_task_id
	 WHERE s.kind_id = ? AND s.state = 'working' AND `

// ListBgAgentsForEpic returns the working background-agent sessions whose
// active_epic_id matches epicID, oldest-first by start time. This is the
// epic-scoped default path of `endless agents`.
func ListBgAgentsForEpic(epicID int64) ([]BgAgent, error) {
	return queryBgAgents(
		bgAgentQuery+`s.active_epic_id = ? ORDER BY s.started_at`,
		int64(sessionkind.SessionKindBackground), epicID,
	)
}

// ListBgAgentsForProject returns the working background-agent sessions in
// projectID, oldest-first by start time. This backs `endless agents --all`,
// which drops the epic filter but stays scoped to the current project.
func ListBgAgentsForProject(projectID int64) ([]BgAgent, error) {
	return queryBgAgents(
		bgAgentQuery+`s.project_id = ? ORDER BY s.started_at`,
		int64(sessionkind.SessionKindBackground), projectID,
	)
}

func queryBgAgents(query string, args ...any) ([]BgAgent, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query bg agents: %w", err)
	}
	defer rows.Close()

	out := []BgAgent{}
	for rows.Next() {
		var a BgAgent
		if err := rows.Scan(&a.ID, &a.ShortID, &a.TaskID, &a.Title, &a.StartedAt); err != nil {
			return nil, fmt.Errorf("scan bg agent: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bg agents: %w", err)
	}
	return out, nil
}

// SessionActiveEpic returns the active_epic_id of sessionID — the epic the
// caller's session is working under — or nil when it is NULL or no such session
// row exists. `endless agents` uses this to auto-resolve the epic to scope by
// when neither --epic nor --all is given (E-1621).
//
// Note: a non-background (tmux/coordinator) session only carries active_epic_id
// once the claim flow records it (E-1624); until that lands, this returns nil
// for interactive callers and the command falls back to its guidance error.
func SessionActiveEpic(sessionID int64) (*int64, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	var epicID *int64
	err = db.QueryRow(
		"SELECT active_epic_id FROM sessions WHERE id = ?", sessionID,
	).Scan(&epicID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve active epic for session %d: %w", sessionID, err)
	}
	return epicID, nil
}
