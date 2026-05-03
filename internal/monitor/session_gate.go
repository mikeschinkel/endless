package monitor

import (
	"database/sql"
	"errors"
	"time"
)

// SetGatePending opens a new gate row for the session (E-971 Layer E).
// If an open gate already exists for the session it is marked
// cleared_by='superseded' before the new row is inserted, so each pivot
// trigger is preserved as telemetry while only one gate stays open at a
// time.
func SetGatePending(sessionID, phrase string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	if _, err = db.Exec(
		`UPDATE session_gates SET cleared_at=?, cleared_by='superseded'
		 WHERE session_id=? AND cleared_at IS NULL`,
		now, sessionID); err != nil {
		return err
	}
	_, err = db.Exec(
		`INSERT INTO session_gates (session_id, matcher_phrase, triggered_at)
		 VALUES (?, ?, ?)`,
		sessionID, phrase, now)
	return err
}

// ClearGatePending marks all open gates for the session as cleared by
// the given verb (task_start | task_confirm | task_add). No-op when the
// session has no open gate.
func ClearGatePending(sessionID, clearedBy string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, err = db.Exec(
		`UPDATE session_gates SET cleared_at=?, cleared_by=?
		 WHERE session_id=? AND cleared_at IS NULL`,
		now, clearedBy, sessionID)
	return err
}

// IsGatePending returns (true, phrase) when the session has an open
// gate. Phrase is the matcher of the most recent open row.
func IsGatePending(sessionID string) (bool, string) {
	db, err := DB()
	if err != nil {
		return false, ""
	}
	var phrase string
	err = db.QueryRow(
		`SELECT matcher_phrase FROM session_gates
		 WHERE session_id=? AND cleared_at IS NULL
		 ORDER BY triggered_at DESC LIMIT 1`,
		sessionID).Scan(&phrase)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ""
		}
		return false, ""
	}
	return true, phrase
}
