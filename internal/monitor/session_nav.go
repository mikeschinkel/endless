package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikeschinkel/endless/internal/navvia"
)

// RecordNav appends one row to the durable session-navigation trail (E-1682):
// a focus change to toPane by the tmux client `client` (the navigator). It is
// the runtime-write path for the global focus-change hook and `session goto`,
// mirroring the mutable `sessions` tier — it writes the local DB directly, NOT
// the committed JSONL ledger.
//
// The new focus (to_*) is resolved from toPane: a tracked Claude session's
// integer id when the pane hosts one, else NULL with the raw pane id retained.
// The prior focus (from_*) is taken from this client's most-recent row's to_*,
// so the hook only needs to report where focus landed — not where it came from.
// project_id follows the destination session (NULL for an untracked pane).
//
// `via` records how the move was triggered (manual vs goto). It fires on every
// focus change, so it stays fast and silent: a move to the pane the client is
// already on (to_pane unchanged from the last row) is a no-op that returns
// (0, nil) without inserting — this also collapses the duplicate event when
// both client-session-changed and session-window-changed fire for one move.
//
// Returns the inserted row id, or 0 when the move was a no-op.
func RecordNav(client, toPane string, via navvia.NavVia) (int64, error) {
	if client == "" {
		return 0, fmt.Errorf("record nav: client required")
	}
	if toPane == "" {
		return 0, fmt.Errorf("record nav: to_pane required")
	}
	db, err := DB()
	if err != nil {
		return 0, err
	}

	// Resolve the destination pane to a live tracked session (+ its project).
	// state != 'ended' guards against pane-id reuse after a tmux server restart
	// (mirrors GetLiveSessionByProcess and the status-line readers).
	var (
		toSessionID *int64
		projectID   *int64
	)
	err = db.QueryRow(
		`SELECT id, project_id FROM sessions
		 WHERE process = ? AND state != 'ended'
		 ORDER BY last_activity DESC LIMIT 1`,
		toPane,
	).Scan(&toSessionID, &projectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("resolve destination pane %s: %w", toPane, err)
	}

	// Prior focus = this client's most-recent destination.
	var (
		lastToSessionID *int64
		lastToPane      sql.NullString
	)
	err = db.QueryRow(
		`SELECT to_session_id, to_pane FROM session_navigations
		 WHERE client = ? ORDER BY id DESC LIMIT 1`,
		client,
	).Scan(&lastToSessionID, &lastToPane)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("read last nav for client %s: %w", client, err)
	}

	// No movement: the client is already focused on this pane. Skip the insert
	// (and absorb the second of the paired focus-change hooks).
	if lastToPane.Valid && lastToPane.String == toPane {
		return 0, nil
	}

	var fromPane *string
	if lastToPane.Valid {
		fromPane = &lastToPane.String
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	res, err := db.Exec(
		`INSERT INTO session_navigations
		   (client, project_id, from_session_id, from_pane, to_session_id, to_pane, via_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		client, projectID, lastToSessionID, fromPane, toSessionID, toPane, int64(via), now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert session_navigation for client %s: %w", client, err)
	}
	return res.LastInsertId()
}

// NavEdge is one rendered row of the navigation trail, intended for JSON
// serialization to the Python `session trail` viewer. The *_session_id and
// *_task_id fields are nil when the endpoint is an untracked pane (or the
// session carries no active task); *Pane is the raw pane id. Via is the slug
// ("manual"/"goto"). CreatedAt is an ISO timestamp the Python side renders
// relative.
type NavEdge struct {
	ID            int64   `json:"id"`
	Client        string  `json:"client"`
	Via           string  `json:"via"`
	CreatedAt     string  `json:"created_at"`
	FromSessionID *int64  `json:"from_session_id"`
	FromPane      *string `json:"from_pane"`
	FromTaskID    *int64  `json:"from_task_id"`
	FromSummary   string  `json:"from_summary"`
	ToSessionID   *int64  `json:"to_session_id"`
	ToPane        string  `json:"to_pane"`
	ToTaskID      *int64  `json:"to_task_id"`
	ToSummary     string  `json:"to_summary"`
}

// ListNavTrail returns navigation-trail rows newest-first, joined to the
// endpoint sessions so the viewer can label each edge with its active task and
// summary (E-1682). When client is non-empty the result is scoped to that tmux
// client; an empty client returns every client's edges (the `--all` surface).
// limit caps the row count (<= 0 falls back to a sane default).
func ListNavTrail(client string, limit int) ([]NavEdge, error) {
	if limit <= 0 {
		limit = 50
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}

	q := `SELECT n.id, n.client, vk.slug, n.created_at,
	             n.from_session_id, n.from_pane, fs.active_task_id, COALESCE(fs.summary, ''),
	             n.to_session_id, n.to_pane, ts.active_task_id, COALESCE(ts.summary, '')
	      FROM session_navigations n
	      JOIN nav_via_kinds vk ON vk.id = n.via_id
	      LEFT JOIN sessions fs ON fs.id = n.from_session_id
	      LEFT JOIN sessions ts ON ts.id = n.to_session_id`
	args := []any{}
	if client != "" {
		q += " WHERE n.client = ?"
		args = append(args, client)
	}
	q += " ORDER BY n.id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query nav trail: %w", err)
	}
	defer rows.Close()

	out := []NavEdge{}
	for rows.Next() {
		var e NavEdge
		if err := rows.Scan(
			&e.ID, &e.Client, &e.Via, &e.CreatedAt,
			&e.FromSessionID, &e.FromPane, &e.FromTaskID, &e.FromSummary,
			&e.ToSessionID, &e.ToPane, &e.ToTaskID, &e.ToSummary,
		); err != nil {
			return nil, fmt.Errorf("scan nav edge: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nav trail: %w", err)
	}
	return out, nil
}
