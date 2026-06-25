// Reopen-context resolution (E-1645). Backs `endless task spawn --reopen` so a
// reopened session inherits the most-applicable prior *ended* session and
// carries that session's read-only restore context (the task's outcome + the
// latest session-status snapshot) into the respawn handoff.
//
// Lives in package events (not monitor) so it can reuse the unexported
// renderSessionStatusMarkdown without exporting it or risking a new import
// cycle — events already depends on monitor for the DB handle.

package events

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// ReopenContext is the read-only context the Python reopen path needs to render
// the respawn handoff. All fields are zero/empty when their source is absent, so
// the caller never has to special-case "no prior session."
type ReopenContext struct {
	// InheritedSessionID is the chosen prior ended session's id, or 0 when no
	// eligible session exists. Live sessions are handled by the caller's
	// liveness guard, never here.
	InheritedSessionID int64 `json:"inherited_session_id"`
	// PriorOutcome is the task's outcome field (empty if unset).
	PriorOutcome string `json:"prior_outcome"`
	// LastStatusSnapshot is the inherited session's latest session_statuses row
	// rendered as markdown (empty when there is no inherited session or it
	// recorded no status).
	LastStatusSnapshot string `json:"last_status_snapshot"`
}

// ResolveReopenContext builds the ReopenContext for a task being reopened.
//
// The inherited-session pick deliberately does NOT order by duration: a stale
// row with a days-long span (Pattern B) would otherwise win. Instead it
// prefers sessions that left real evidence of work — a populated process or
// transcript_path, or a span of at least 10 seconds — and among those takes the
// most recently started. Sub-10s ghosts (E-1640) with no evidence sort last and
// are only chosen when nothing better exists.
func ResolveReopenContext(taskID int64) (ReopenContext, error) {
	db, err := monitor.DB()
	if err != nil {
		return ReopenContext{}, err
	}
	return reopenContext(db, taskID)
}

// reopenContext is the db-taking core of ResolveReopenContext, split out so
// tests can drive it against a schema-applied temp DB without monitor's global
// connection (the same pattern the exec* handlers use).
func reopenContext(db *sql.DB, taskID int64) (ReopenContext, error) {
	var ctx ReopenContext
	var err error

	ctx.PriorOutcome, err = taskOutcome(db, taskID)
	if err != nil {
		return ctx, err
	}

	ctx.InheritedSessionID, err = inheritedSessionID(db, taskID)
	if err != nil {
		return ctx, err
	}

	if ctx.InheritedSessionID != 0 {
		ctx.LastStatusSnapshot, err = latestStatusSnapshot(db, ctx.InheritedSessionID)
		if err != nil {
			return ctx, err
		}
	}

	return ctx, nil
}

// taskOutcome returns the task's outcome field ("" if unset or no such task).
func taskOutcome(db *sql.DB, taskID int64) (string, error) {
	var outcome string
	err := db.QueryRow(
		"SELECT COALESCE(outcome, '') FROM tasks WHERE id = ?", taskID,
	).Scan(&outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("events: read outcome for E-%d: %w", taskID, err)
	}
	return outcome, nil
}

// inheritedSessionID picks the most-applicable prior ended session for a task,
// skipping evidence-free sub-10s ghosts. Returns 0 when none exists.
func inheritedSessionID(db *sql.DB, taskID int64) (int64, error) {
	var id int64
	err := db.QueryRow(
		`SELECT id FROM sessions
		 WHERE active_task_id = ? AND state = 'ended'
		 ORDER BY (process IS NOT NULL
		           OR transcript_path IS NOT NULL
		           OR (julianday(last_activity) - julianday(started_at)) * 86400 >= 10) DESC,
		          started_at DESC
		 LIMIT 1`,
		taskID,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("events: resolve inherited session for E-%d: %w", taskID, err)
	}
	return id, nil
}

// latestStatusSnapshot renders the most recent session_statuses row for a
// session as markdown, reusing the same renderer the chat display uses. Returns
// "" when the session recorded no status.
func latestStatusSnapshot(db *sql.DB, sessionID int64) (string, error) {
	var p SessionStatusRecordedPayload
	err := db.QueryRow(
		`SELECT COALESCE(headline, ''), COALESCE(tasks, ''), COALESCE(decisions, ''),
		        COALESCE(commits, ''), COALESCE(memory, ''), COALESCE(summary, ''),
		        COALESCE(notes, '')
		 FROM session_statuses
		 WHERE session_id = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`,
		sessionID,
	).Scan(&p.Headline, &p.Tasks, &p.Decisions, &p.Commits, &p.Memory, &p.Summary, &p.Notes)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("events: read latest status for session %d: %w", sessionID, err)
	}
	return renderSessionStatusMarkdown(&p), nil
}
