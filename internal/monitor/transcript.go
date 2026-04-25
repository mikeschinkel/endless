package monitor

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

// transcriptLine represents one line of a Claude Code JSONL transcript.
type transcriptLine struct {
	Type      string          `json:"type"`
	UUID      string          `json:"uuid"`
	Timestamp string          `json:"timestamp"`
	SessionID string          `json:"sessionId"`
	Message   json.RawMessage `json:"message"`
}

// transcriptMessage is the nested message object inside a transcript line.
type transcriptMessage struct {
	Role    string            `json:"role"`
	Content json.RawMessage   `json:"content"`
}

// contentBlock represents one block in an assistant message's content array.
type contentBlock struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Name    string `json:"name"`
	Input   json.RawMessage `json:"input"`
}

// ParseTranscript reads new lines from a JSONL transcript file and inserts
// messages into the session_messages table. Uses offset-based incremental
// reading to avoid re-parsing the entire file on each call.
func ParseTranscript(sessionID, transcriptPath string) error {
	if transcriptPath == "" {
		return nil
	}

	db, err := DB()
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}

	// Get the last-read offset
	offset, err := getTranscriptOffset(db, sessionID)
	if err != nil {
		return fmt.Errorf("getting offset: %w", err)
	}

	// Open the transcript file
	f, err := os.Open(transcriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // file may not exist yet on SessionStart
		}
		return fmt.Errorf("opening transcript %s: %w", transcriptPath, err)
	}
	defer f.Close()

	// Seek to the last-read position
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return fmt.Errorf("seeking to offset %d: %w", offset, err)
		}
	}

	// Read new lines
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB max line
	var summarySet bool

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var tl transcriptLine
		if err := json.Unmarshal(line, &tl); err != nil {
			continue // skip malformed lines
		}

		// Skip non-message types
		if tl.Type != "user" && tl.Type != "assistant" {
			continue
		}
		if tl.UUID == "" || tl.Message == nil {
			continue
		}

		var msg transcriptMessage
		if err := json.Unmarshal(tl.Message, &msg); err != nil {
			continue
		}

		switch {
		case tl.Type == "user" && msg.Role == "user":
			text := extractUserText(msg.Content)
			if text == "" {
				continue
			}
			// Skip tool results, system messages, meta
			if strings.HasPrefix(text, "<") || strings.HasPrefix(text, "{\"tool_use_id\"") {
				continue
			}
			insertMessage(db, sessionID, "user", text, "", tl.UUID, tl.Timestamp)

		case tl.Type == "assistant" && msg.Role == "assistant":
			texts, tools := extractAssistantContent(msg.Content)
			// Insert text blocks as assistant message
			if texts != "" {
				insertMessage(db, sessionID, "assistant", texts, "", tl.UUID, tl.Timestamp)
				// Set summary from first assistant response
				if !summarySet {
					setSummaryIfEmpty(db, sessionID, texts)
					summarySet = true
				}
			}
			// Insert tool_use blocks as separate messages
			for _, tool := range tools {
				toolUUID := tl.UUID + ":" + tool.name
				insertMessage(db, sessionID, "tool_use", tool.summary, tool.name, toolUUID, tl.Timestamp)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("scanning transcript: %v", err)
	}

	// Update offset to current file position
	newOffset, err := f.Seek(0, io.SeekCurrent)
	if err == nil && newOffset > offset {
		setTranscriptOffset(db, sessionID, newOffset)
	}

	return nil
}

// ParseTranscriptFull re-reads a transcript from the beginning (offset 0).
// Used for reimporting/backfilling.
func ParseTranscriptFull(sessionID, transcriptPath string) error {
	if transcriptPath == "" {
		return nil
	}

	db, err := DB()
	if err != nil {
		return err
	}

	// Reset offset to 0
	setTranscriptOffset(db, sessionID, 0)

	return ParseTranscript(sessionID, transcriptPath)
}

type toolInfo struct {
	name    string
	summary string
}

// extractUserText gets the text content from a user message.
// User messages can be a plain string or an array of content blocks.
func extractUserText(content json.RawMessage) string {
	// Try as plain string first
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return strings.TrimSpace(text)
	}

	// Try as array of content blocks
	var blocks []json.RawMessage
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}

	var parts []string
	for _, block := range blocks {
		var cb struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(block, &cb); err != nil {
			continue
		}
		if cb.Type == "text" && cb.Text != "" {
			parts = append(parts, cb.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// extractAssistantContent separates text blocks and tool_use blocks
// from an assistant message's content array.
func extractAssistantContent(content json.RawMessage) (string, []toolInfo) {
	var blocks []contentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return "", nil
	}

	var textParts []string
	var tools []toolInfo

	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			summary := block.Name
			// Add truncated input for context
			if block.Input != nil {
				inputStr := string(block.Input)
				if len(inputStr) > 500 {
					inputStr = inputStr[:500] + "..."
				}
				summary += ": " + inputStr
			}
			tools = append(tools, toolInfo{
				name:    block.Name,
				summary: summary,
			})
		// Skip: thinking, signature, etc.
		}
	}

	return strings.Join(textParts, "\n"), tools
}

func getTranscriptOffset(db *sql.DB, sessionID string) (int64, error) {
	var offset int64
	err := db.QueryRow(
		"SELECT transcript_offset FROM sessions WHERE session_id = ?",
		sessionID,
	).Scan(&offset)
	if err != nil {
		return 0, nil // session may not exist yet
	}
	return offset, nil
}

func setTranscriptOffset(db *sql.DB, sessionID string, offset int64) {
	db.Exec(
		"UPDATE sessions SET transcript_offset = ? WHERE session_id = ?",
		offset, sessionID,
	)
}

func setSummaryIfEmpty(db *sql.DB, sessionID, text string) {
	// Only set if summary is currently empty
	var current sql.NullString
	err := db.QueryRow(
		"SELECT summary FROM sessions WHERE session_id = ?",
		sessionID,
	).Scan(&current)
	if err != nil || (current.Valid && current.String != "") {
		return
	}

	// Extract first 1-2 sentences as summary
	summary := text
	if len(summary) > 200 {
		// Find sentence boundary
		cutoff := 200
		for i := cutoff; i > 100; i-- {
			if summary[i] == '.' || summary[i] == '!' || summary[i] == '?' {
				cutoff = i + 1
				break
			}
		}
		summary = summary[:cutoff]
	}
	summary = strings.TrimSpace(summary)

	db.Exec(
		"UPDATE sessions SET summary = ? WHERE session_id = ? AND (summary IS NULL OR summary = '')",
		summary, sessionID,
	)
}

func insertMessage(db *sql.DB, sessionID, role, content, toolName, uuid, timestamp string) {
	if content == "" {
		return
	}
	db.Exec(
		`INSERT OR IGNORE INTO session_messages
		 (session_id, role, content, tool_name, message_uuid, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, role, content, nilIfEmpty(toolName), uuid, timestamp,
	)
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// SetTranscriptPath stores the transcript file path for a session.
func SetTranscriptPath(sessionID, path string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		"UPDATE sessions SET transcript_path = ? WHERE session_id = ?",
		path, sessionID,
	)
	return err
}

// GetTranscriptPath retrieves the stored transcript path for a session.
func GetTranscriptPath(sessionID string) string {
	db, err := DB()
	if err != nil {
		return ""
	}
	var path sql.NullString
	db.QueryRow(
		"SELECT transcript_path FROM sessions WHERE session_id = ?",
		sessionID,
	).Scan(&path)
	if path.Valid {
		return path.String
	}
	return ""
}
