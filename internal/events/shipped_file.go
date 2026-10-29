package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// ShippedFileWrittenPayload is shipped_file.written's payload (E-2421): which
// shipped file `endless project init` wrote into the project, and the hash of
// exactly the bytes it wrote. The path rides in the entity ID.
type ShippedFileWrittenPayload struct {
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

func execShippedFileWritten(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	projectID, err := resolveProjectID(db, evt.Project)
	if err != nil {
		return nil, err
	}
	if err := applyShippedFileWritten(db, evt, projectID); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

func replayShippedFileWritten(db *sql.DB, evt *Event, _ *ProjectResult) error {
	projectID, err := ensureProject(db, evt.Project)
	if err != nil {
		return err
	}
	return applyShippedFileWritten(db, evt, projectID)
}

// applyShippedFileWritten keeps one row per (project, path): the latest write
// is the only one a later init compares against.
func applyShippedFileWritten(db dbQuerier, evt *Event, projectID int64) error {
	var p ShippedFileWrittenPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("events: unmarshal %s payload: %w", evt.Kind, err)
	}
	path := strings.TrimSpace(evt.Entity.ID)
	if path == "" || strings.TrimSpace(p.Kind) == "" || strings.TrimSpace(p.SHA256) == "" {
		return fmt.Errorf("events: %s: missing path, kind or sha256", evt.Kind)
	}
	_, err := db.Exec(`
		INSERT INTO shipped_file_writes (project_id, path, kind, sha256, written_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (project_id, path) DO UPDATE SET
			kind = excluded.kind,
			sha256 = excluded.sha256,
			written_at = excluded.written_at`,
		projectID, path, p.Kind, p.SHA256, kairosToISO(evt.TS))
	if err != nil {
		return fmt.Errorf("events: %s: %w", evt.Kind, err)
	}
	return nil
}

// RecordedShippedFile returns the hash of what `endless project init` last
// wrote at path in the named project, or "" when it has no record.
func RecordedShippedFile(db dbQuerier, project, path string) (string, error) {
	var hash string
	err := db.QueryRow(`
		SELECT w.sha256 FROM shipped_file_writes w
		JOIN live_projects p ON p.id = w.project_id
		WHERE p.name = ? AND w.path = ?`, project, path).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("events: read shipped file record: %w", err)
	}
	return hash, nil
}
