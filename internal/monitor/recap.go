package monitor

import (
	"database/sql"
	"fmt"
	"os/exec"
	"strings"
)

const recapPrompt = `Write a one-line summary of this conversation (max 200 chars). ` +
	`The first 60 characters must identify WHAT was worked on — ` +
	`a specific feature name, task ID, bug fix, or component. ` +
	`Examples of good starts: 'Added task search command (E-730)', ` +
	`'Fixed SQLite migration data loss in sessions table', ` +
	`'Designed session recap feature with hook-driven capture'. ` +
	`Examples of BAD starts: 'Let me read the file', ` +
	`'Discussed various topics', 'Worked on improvements', ` +
	`'The conversation covered'. ` +
	`No filler, no preamble. Pure substance.`

const recapMessageLimit = 20
const recapContentLimit = 1000
const recapMinNewMessages = 10

// RecapSession generates a recap for a single session.
// Returns the recap text, or empty string if skipped/error.
func RecapSession(sessionID string, force bool) (string, error) {
	db, err := DB()
	if err != nil {
		return "", fmt.Errorf("opening db: %w", err)
	}

	// Get session info
	var dbID int64
	var summarySeq int
	err = db.QueryRow(
		"SELECT id, COALESCE(summary_seq, 0) FROM sessions WHERE session_id = ?",
		sessionID,
	).Scan(&dbID, &summarySeq)
	if err != nil {
		return "", fmt.Errorf("session not found: %w", err)
	}

	// Count user messages
	var userCount int
	db.QueryRow(
		"SELECT count(*) FROM session_messages WHERE session_id = ? AND role = 'user'",
		sessionID,
	).Scan(&userCount)

	// Skip if not enough new messages (unless forced)
	if !force && userCount-summarySeq < recapMinNewMessages {
		return "", nil
	}

	// Get last N user+assistant messages
	rows, err := db.Query(
		"SELECT role, content FROM session_messages "+
			"WHERE session_id = ? AND role IN ('user', 'assistant') "+
			"ORDER BY created_at DESC LIMIT ?",
		sessionID, recapMessageLimit,
	)
	if err != nil {
		return "", fmt.Errorf("querying messages: %w", err)
	}
	defer rows.Close()

	// Collect in reverse order (query returns newest first)
	type msg struct {
		role, content string
	}
	var msgs []msg
	for rows.Next() {
		var m msg
		rows.Scan(&m.role, &m.content)
		msgs = append(msgs, m)
	}
	if len(msgs) == 0 {
		return "", nil
	}

	// Reverse to chronological
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}

	// Build conversation text
	var parts []string
	for _, m := range msgs {
		role := "User"
		if m.role == "assistant" {
			role = "Claude"
		}
		content := m.content
		if len(content) > recapContentLimit {
			content = content[:recapContentLimit] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s: %s", role, content))
	}
	transcript := strings.Join(parts, "\n\n")

	// Call claude -p
	prompt := recapPrompt + "\n\n" + transcript
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("claude CLI not found: %w", err)
	}

	cmd := exec.Command(claudeBin, "-p", prompt, "--allowedTools", "")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude -p failed: %w", err)
	}

	summary := strings.TrimSpace(string(out))
	if summary == "" {
		return "", nil
	}

	// Store recap and update watermark
	db.Exec(
		"UPDATE sessions SET summary = ?, summary_seq = ?, needs_recap = 0 WHERE session_id = ?",
		summary, userCount, sessionID,
	)

	return summary, nil
}

// RecapSessionByID resolves a session by integer ID and generates a recap.
func RecapSessionByID(id int64, force bool) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}

	var sessionID string
	err = db.QueryRow("SELECT session_id FROM sessions WHERE id = ?", id).Scan(&sessionID)
	if err != nil {
		return "", fmt.Errorf("session %d not found: %w", id, err)
	}

	return RecapSession(sessionID, force)
}

// GetSessionsNeedingRecap returns session IDs that need recap generation.
func GetSessionsNeedingRecap() []string {
	db, err := DB()
	if err != nil {
		return nil
	}

	rows, err := db.Query(
		"SELECT session_id FROM sessions WHERE needs_recap = 1 AND hidden = 0",
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids
}

// RecapOneStale generates a recap for the single most stale session
// that needs one. Returns the session DB ID and recap, or 0/"" if none.
func RecapOneStale() (int64, string, error) {
	db, err := DB()
	if err != nil {
		return 0, "", err
	}

	var sessionID string
	var dbID int64
	err = db.QueryRow(
		"SELECT s.id, s.session_id FROM sessions s " +
			"WHERE s.needs_recap = 1 AND s.hidden = 0 " +
			"ORDER BY s.last_activity ASC LIMIT 1",
	).Scan(&dbID, &sessionID)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}

	summary, err := RecapSession(sessionID, false)
	return dbID, summary, err
}
