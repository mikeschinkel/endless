package monitor

import (
	"encoding/json"
	"time"
)

// RecordActivity logs an activity event for a project.
func RecordActivity(projectID int64, source, workingDir string, sessionCtx map[string]string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	var ctxJSON *string
	if len(sessionCtx) > 0 {
		b, err := json.Marshal(sessionCtx)
		if err == nil {
			s := string(b)
			ctxJSON = &s
		}
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, err = db.Exec(
		"INSERT INTO activity (project_id, source, working_dir, session_context, created_at) "+
			"VALUES (?, ?, ?, ?, ?)",
		projectID, source, workingDir, ctxJSON, now,
	)
	return err
}

// ShouldThrottle returns true if the last activity for this project+source
// was less than intervalSec seconds ago.
func ShouldThrottle(projectID int64, source string, intervalSec int) (bool, error) {
	db, err := DB()
	if err != nil {
		return false, err
	}

	var lastRun string
	err = db.QueryRow(
		"SELECT created_at FROM activity "+
			"WHERE project_id = ? AND source = ? "+
			"ORDER BY created_at DESC LIMIT 1",
		projectID, source,
	).Scan(&lastRun)
	if err != nil {
		// No previous run
		return false, nil
	}

	t, err := time.Parse("2006-01-02T15:04:05", lastRun)
	if err != nil {
		return false, nil
	}

	return time.Since(t).Seconds() < float64(intervalSec), nil
}
