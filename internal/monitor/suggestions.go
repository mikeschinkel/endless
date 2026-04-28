package monitor

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// suggestionBannerRe matches `**SUGGESTION (source):** body` lines emitted
// by Claude in response to enforcement blocks. The body extends to end of line.
var suggestionBannerRe = regexp.MustCompile(`\*\*SUGGESTION \(([a-z_]+)\):\*\*\s*([^\n]+)`)

// ScanRecentSuggestions scans recent assistant messages for the session and
// records any new SUGGESTION banners as open suggestions.
// Dedupes by exact (session, source, suggestion) match against existing rows.
//
// IMPORTANT: this function reads rows fully into memory before issuing any
// further DB calls. The Endless DB is configured with SetMaxOpenConns(1)
// because SQLite is single-writer, so iterating rows while issuing nested
// queries causes a goroutine deadlock (the second query blocks waiting for
// the connection that the iterator is holding).
func ScanRecentSuggestions(sessionID string, projectID int64) error {
	db, err := DB()
	if err != nil {
		return err
	}

	// Phase 1: drain the row iterator before issuing any other DB calls.
	rows, err := db.Query(
		`SELECT content FROM session_messages
		 WHERE session_id=? AND role='assistant'
		 ORDER BY created_at DESC LIMIT 20`,
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("loading recent assistant messages: %w", err)
	}
	var contents []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err == nil {
			contents = append(contents, content)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating assistant messages: %w", err)
	}

	// Phase 2: extract unique banner matches from the in-memory contents.
	type banner struct{ source, body string }
	seen := make(map[string]bool)
	var banners []banner
	for _, content := range contents {
		for _, m := range suggestionBannerRe.FindAllStringSubmatch(content, -1) {
			source := m[1]
			body := strings.TrimSpace(m[2])
			key := source + "|" + body
			if seen[key] {
				continue
			}
			seen[key] = true
			banners = append(banners, banner{source, body})
		}
	}

	// Phase 3: dedup against the DB and insert. Each call uses the single
	// connection sequentially (no nested iteration).
	for _, b := range banners {
		var exists int
		err := db.QueryRow(
			`SELECT 1 FROM suggestions WHERE session_id=? AND source=? AND suggestion=? LIMIT 1`,
			sessionID, b.source, b.body,
		).Scan(&exists)
		if err == nil {
			continue
		}
		if err := RecordSuggestion(sessionID, projectID, b.source, "", b.body); err != nil {
			return err
		}
	}
	return nil
}

// Suggestion is one row from the suggestions table.
// TaskID is non-nil when the suggestion has been accepted into a task.
type Suggestion struct {
	ID         int64
	SessionID  string
	ProjectID  *int64
	Source     string
	TriggerCtx string
	Suggestion string
	CreatedAt  string
	TaskID     *int64
	Notes      string
}

// RecordSuggestion writes a new open suggestion (task_id NULL) to the DB.
func RecordSuggestion(sessionID string, projectID int64, source, triggerCtx, suggestion string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	var pid any
	if projectID > 0 {
		pid = projectID
	}
	_, err = db.Exec(
		`INSERT INTO suggestions (session_id, project_id, source, trigger_ctx, suggestion)
		 VALUES (?, ?, ?, ?, ?)`,
		sessionID, pid, source, triggerCtx, suggestion,
	)
	if err != nil {
		return fmt.Errorf("recording suggestion: %w", err)
	}
	return nil
}

// ListOpenSuggestions returns suggestions for the project that have not been
// accepted (task_id IS NULL). Pass projectID=0 to list across all projects.
func ListOpenSuggestions(projectID int64) ([]Suggestion, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	var rows *sql.Rows
	if projectID > 0 {
		rows, err = db.Query(
			`SELECT id, session_id, project_id, source, trigger_ctx, suggestion, created_at, task_id, notes
			 FROM suggestions
			 WHERE project_id=? AND task_id IS NULL
			 ORDER BY created_at DESC`,
			projectID,
		)
	} else {
		rows, err = db.Query(
			`SELECT id, session_id, project_id, source, trigger_ctx, suggestion, created_at, task_id, notes
			 FROM suggestions
			 WHERE task_id IS NULL
			 ORDER BY created_at DESC`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("listing suggestions: %w", err)
	}
	defer rows.Close()
	return scanSuggestions(rows)
}

// ListAllSuggestions returns every suggestion (open and accepted) for the project.
func ListAllSuggestions(projectID int64) ([]Suggestion, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT id, session_id, project_id, source, trigger_ctx, suggestion, created_at, task_id, notes
		 FROM suggestions WHERE project_id=? ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing all suggestions: %w", err)
	}
	defer rows.Close()
	return scanSuggestions(rows)
}

// GetSuggestion fetches one suggestion by ID.
func GetSuggestion(id int64) (*Suggestion, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT id, session_id, project_id, source, trigger_ctx, suggestion, created_at, task_id, notes
		 FROM suggestions WHERE id=? LIMIT 1`,
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("loading suggestion %d: %w", id, err)
	}
	defer rows.Close()
	out, err := scanSuggestions(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("suggestion %d not found", id)
	}
	return &out[0], nil
}

// AcceptSuggestion sets the suggestion's task_id, marking it accepted.
func AcceptSuggestion(suggestionID, taskID int64) error {
	db, err := DB()
	if err != nil {
		return err
	}
	res, err := db.Exec(
		`UPDATE suggestions SET task_id=? WHERE id=? AND task_id IS NULL`,
		taskID, suggestionID,
	)
	if err != nil {
		return fmt.Errorf("accepting suggestion %d: %w", suggestionID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("suggestion %d not found or already accepted", suggestionID)
	}
	return nil
}

// CountOpenSuggestions returns the count of open suggestions for the project.
func CountOpenSuggestions(projectID int64) (int, error) {
	db, err := DB()
	if err != nil {
		return 0, err
	}
	var n int
	err = db.QueryRow(
		`SELECT count(*) FROM suggestions WHERE project_id=? AND task_id IS NULL`,
		projectID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting suggestions: %w", err)
	}
	return n, nil
}

func scanSuggestions(rows *sql.Rows) ([]Suggestion, error) {
	var out []Suggestion
	for rows.Next() {
		var s Suggestion
		var pid, tid sql.NullInt64
		var trigger, notes sql.NullString
		if err := rows.Scan(
			&s.ID, &s.SessionID, &pid, &s.Source,
			&trigger, &s.Suggestion, &s.CreatedAt, &tid, &notes,
		); err != nil {
			return nil, fmt.Errorf("scanning suggestion: %w", err)
		}
		if pid.Valid {
			v := pid.Int64
			s.ProjectID = &v
		}
		if tid.Valid {
			v := tid.Int64
			s.TaskID = &v
		}
		s.TriggerCtx = trigger.String
		s.Notes = notes.String
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating suggestions: %w", err)
	}
	return out, nil
}
