package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikeschinkel/endless/internal/gatekind"
)

// session_gate.go holds the direct-db.Exec helpers backing the pause-on-revisit
// hook (E-1542). session_gates is ephemeral session-scoped state — like
// sessions, activity, and channels it is written directly here, not through the
// event ledger (its audit lives in the table's own triggered_at/cleared_* cols).
// The 'revisit' kind is the only kind at v1; other kinds add their own helpers.

// NearestRevisitEpicAncestor walks up tasks.parent_id from taskID and returns
// the id of the nearest ancestor that is an epic currently in status='revisit'.
// taskID itself is included in the walk (depth 0). The depth is capped at 32 so
// a malformed parent_id cycle terminates instead of looping forever. found is
// false when no such ancestor exists.
func NearestRevisitEpicAncestor(taskID int64) (epicID int64, found bool, err error) {
	db, err := DB()
	if err != nil {
		return 0, false, err
	}
	const q = `
		WITH RECURSIVE ancestry(id, parent_id, type_id, status, depth) AS (
			SELECT id, parent_id, type_id, status, 0 FROM tasks WHERE id = ?
			UNION ALL
			SELECT t.id, t.parent_id, t.type_id, t.status, a.depth + 1
			FROM tasks t JOIN ancestry a ON t.id = a.parent_id
			WHERE a.depth < 32
		)
		SELECT a.id
		FROM ancestry a
		JOIN task_types tt ON tt.id = a.type_id
		WHERE tt.slug = 'epic' AND a.status = 'revisit'
		ORDER BY a.depth
		LIMIT 1`
	err = db.QueryRow(q, taskID).Scan(&epicID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("resolve revisit epic ancestor for E-%d: %w", taskID, err)
	}
	return epicID, true, nil
}

// SetRevisitGate opens a revisit gate for the session against epicID. Any prior
// open revisit gate for the session is first cleared with cleared_by='superseded'
// so at most one open row exists per (session_id, kind=revisit).
func SetRevisitGate(sessionID, epicID int64) error {
	db, err := DB()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	if _, err = db.Exec(
		`UPDATE session_gates SET cleared_at=?, cleared_by='superseded'
		 WHERE session_id=? AND kind_id=? AND cleared_at IS NULL`,
		now, sessionID, int(gatekind.GateKindRevisit),
	); err != nil {
		return fmt.Errorf("supersede open revisit gate for session %d: %w", sessionID, err)
	}
	if _, err = db.Exec(
		`INSERT INTO session_gates (session_id, kind_id, epic_id, triggered_at)
		 VALUES (?, ?, ?, ?)`,
		sessionID, int(gatekind.GateKindRevisit), epicID, now,
	); err != nil {
		return fmt.Errorf("insert revisit gate for session %d: %w", sessionID, err)
	}
	return nil
}

// PendingRevisitGate returns the epic id of the session's open revisit gate, if
// any. found is false when the session has no open revisit gate.
func PendingRevisitGate(sessionID int64) (epicID int64, found bool, err error) {
	db, err := DB()
	if err != nil {
		return 0, false, err
	}
	var epic sql.NullInt64
	err = db.QueryRow(
		`SELECT epic_id FROM session_gates
		 WHERE session_id=? AND kind_id=? AND cleared_at IS NULL
		 ORDER BY id DESC LIMIT 1`,
		sessionID, int(gatekind.GateKindRevisit),
	).Scan(&epic)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query pending revisit gate for session %d: %w", sessionID, err)
	}
	return epic.Int64, true, nil
}

// ClearRevisitGate closes the session's open revisit gate(s) with the given
// cleared_by reason (revisit_continue, revisit_pause, or revisit_resolved) and
// returns the number of rows cleared — 0 means there was no pending prompt.
func ClearRevisitGate(sessionID int64, clearedBy string) (cleared int, err error) {
	db, err := DB()
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	res, err := db.Exec(
		`UPDATE session_gates SET cleared_at=?, cleared_by=?
		 WHERE session_id=? AND kind_id=? AND cleared_at IS NULL`,
		now, clearedBy, sessionID, int(gatekind.GateKindRevisit),
	)
	if err != nil {
		return 0, fmt.Errorf("clear revisit gate for session %d: %w", sessionID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revisit gate rows affected: %w", err)
	}
	return int(n), nil
}
