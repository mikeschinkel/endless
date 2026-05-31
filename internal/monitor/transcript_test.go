package monitor

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// seedTranscriptSession inserts a minimal sessions row so session_messages
// inserts (which FK on sessions.session_id) and the UPDATE statements run by
// ParseTranscript / SetTranscriptPath / FlagNeedsRecap have a target row.
func seedTranscriptSession(t *testing.T, db *sql.DB, sessionID string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, platform, state, last_activity)
		 VALUES (?, 'claude', 'working', '2026-05-29T00:00:00')`,
		sessionID,
	); err != nil {
		t.Fatalf("seed session %q: %v", sessionID, err)
	}
}

// writeTranscript serializes the given entries one-per-line as JSONL into
// path. Each entry is a map so tests can declare line shapes inline without
// pulling in transcriptLine's unexported fields. Uses os.WriteFile to mirror
// the all-at-once write a freshly-rotated Claude transcript produces.
func writeTranscript(t *testing.T, path string, entries []map[string]any) {
	t.Helper()
	var buf []byte
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("write transcript %s: %v", path, err)
	}
}

// appendTranscript opens path for append and writes additional entries as
// new JSONL lines. Used to model the streaming-append shape ParseTranscript's
// incremental offset is designed for.
func appendTranscript(t *testing.T, path string, entries []map[string]any) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript for append %s: %v", path, err)
	}
	defer f.Close()
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatalf("append entry: %v", err)
		}
	}
}

// countMessages returns the total count of session_messages rows for sid.
func countMessages(t *testing.T, db *sql.DB, sid string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT count(*) FROM session_messages WHERE session_id = ?", sid,
	).Scan(&n); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	return n
}

// userAssistantBlock returns a JSONL entry shaped like a Claude transcript's
// "assistant" line (content as an array of typed blocks). Text-only.
func assistantTextEntry(uuid, sid, text string) map[string]any {
	return map[string]any{
		"type":      "assistant",
		"uuid":      uuid,
		"timestamp": "2026-05-29T00:00:00",
		"sessionId": sid,
		"message": map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": text},
			},
		},
	}
}

// userStringEntry returns a JSONL entry shaped like a Claude transcript's
// "user" line whose content is a plain string (the simplest extractUserText
// branch).
func userStringEntry(uuid, sid, text string) map[string]any {
	return map[string]any{
		"type":      "user",
		"uuid":      uuid,
		"timestamp": "2026-05-29T00:00:00",
		"sessionId": sid,
		"message": map[string]any{
			"role":    "user",
			"content": text,
		},
	}
}

// TestParseTranscript_EmptyFileNoRows pins the no-op branch: an empty
// transcript file produces no error and inserts no session_messages rows.
func TestParseTranscript_EmptyFileNoRows(t *testing.T) {
	db := withTestDB(t)
	seedTranscriptSession(t, db, "sess-empty")

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeTranscript(t, path, nil)

	if err := ParseTranscript("sess-empty", path); err != nil {
		t.Fatalf("ParseTranscript on empty file: %v", err)
	}
	if got := countMessages(t, db, "sess-empty"); got != 0 {
		t.Errorf("empty transcript inserted %d rows, want 0", got)
	}
}

// TestParseTranscript_MissingFileNoError pins the os.IsNotExist branch:
// a non-existent transcript path returns nil (the file may not exist yet
// on SessionStart) and produces no rows.
func TestParseTranscript_MissingFileNoError(t *testing.T) {
	db := withTestDB(t)
	seedTranscriptSession(t, db, "sess-missing")

	missing := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	if err := ParseTranscript("sess-missing", missing); err != nil {
		t.Fatalf("ParseTranscript on missing file: %v", err)
	}
	if got := countMessages(t, db, "sess-missing"); got != 0 {
		t.Errorf("missing transcript inserted %d rows, want 0", got)
	}
}

// TestParseTranscript_EmptyPathNoOp pins the early return when transcriptPath
// is "" — callers (e.g. SessionStart before the path is bound) must not error.
func TestParseTranscript_EmptyPathNoOp(t *testing.T) {
	withTestDB(t)
	if err := ParseTranscript("sess-no-path", ""); err != nil {
		t.Errorf("ParseTranscript with empty path: %v, want nil", err)
	}
}

// TestParseTranscript_UserAndAssistantInserted pins the happy path: one user
// (plain-string content) and one assistant (text-block content) line each
// land in session_messages with the right role, content, and uuid.
func TestParseTranscript_UserAndAssistantInserted(t *testing.T) {
	db := withTestDB(t)
	const sid = "sess-happy"
	seedTranscriptSession(t, db, sid)

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeTranscript(t, path, []map[string]any{
		userStringEntry("uuid-u1", sid, "hello"),
		assistantTextEntry("uuid-a1", sid, "world"),
	})

	if err := ParseTranscript(sid, path); err != nil {
		t.Fatalf("ParseTranscript: %v", err)
	}

	rows, err := db.Query(
		"SELECT role, content, message_uuid FROM session_messages WHERE session_id=? ORDER BY id",
		sid,
	)
	if err != nil {
		t.Fatalf("query messages: %v", err)
	}
	defer rows.Close()

	type row struct{ role, content, uuid string }
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.role, &r.content, &r.uuid); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	want := []row{
		{"user", "hello", "uuid-u1"},
		{"assistant", "world", "uuid-a1"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestParseTranscript_ToolUseInserted pins the tool_use branch of
// extractAssistantContent: an assistant message containing a tool_use block
// inserts a tool_use row (alongside the text row) with the tool name in
// tool_name and a synthetic uuid (`<line uuid>:<tool name>`).
func TestParseTranscript_ToolUseInserted(t *testing.T) {
	db := withTestDB(t)
	const sid = "sess-tool"
	seedTranscriptSession(t, db, sid)

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeTranscript(t, path, []map[string]any{
		{
			"type":      "assistant",
			"uuid":      "uuid-a1",
			"timestamp": "2026-05-29T00:00:00",
			"sessionId": sid,
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "text", "text": "running tool"},
					{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "ls"}},
				},
			},
		},
	})

	if err := ParseTranscript(sid, path); err != nil {
		t.Fatalf("ParseTranscript: %v", err)
	}

	rows, err := db.Query(
		"SELECT role, tool_name FROM session_messages WHERE session_id=? ORDER BY id",
		sid,
	)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	type row struct {
		role string
		tool sql.NullString
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.role, &r.tool); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 (text + tool_use): %#v", len(got), got)
	}
	if got[0].role != "assistant" || got[0].tool.Valid {
		t.Errorf("row 0 = %+v, want role=assistant tool_name=NULL", got[0])
	}
	if got[1].role != "tool_use" || got[1].tool.String != "Bash" {
		t.Errorf("row 1 = %+v, want role=tool_use tool_name=Bash", got[1])
	}
}

// TestParseTranscript_MalformedLineSkipped pins the parser's leniency: a
// non-JSON line in the middle of the file is silently skipped (per the
// `continue // skip malformed lines` branch) and surrounding good lines
// still parse cleanly.
func TestParseTranscript_MalformedLineSkipped(t *testing.T) {
	db := withTestDB(t)
	const sid = "sess-malformed"
	seedTranscriptSession(t, db, sid)

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	// Hand-build the file: good line, garbage line, good line.
	good1, _ := json.Marshal(userStringEntry("uuid-u1", sid, "before"))
	good2, _ := json.Marshal(assistantTextEntry("uuid-a1", sid, "after"))
	body := append(good1, '\n')
	body = append(body, []byte("not-json{{{\n")...)
	body = append(body, good2...)
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := ParseTranscript(sid, path); err != nil {
		t.Fatalf("ParseTranscript: %v", err)
	}
	if got := countMessages(t, db, sid); got != 2 {
		t.Errorf("expected 2 rows around the malformed line, got %d", got)
	}
}

// TestParseTranscript_IncrementalOffsetSkipsAlreadyRead pins the offset
// semantics: a second ParseTranscript call after the file grew only inserts
// rows for the appended lines (the bytes already read on the first call are
// skipped via the persisted transcript_offset).
func TestParseTranscript_IncrementalOffsetSkipsAlreadyRead(t *testing.T) {
	db := withTestDB(t)
	const sid = "sess-incr"
	seedTranscriptSession(t, db, sid)

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeTranscript(t, path, []map[string]any{
		userStringEntry("uuid-u1", sid, "first"),
	})
	if err := ParseTranscript(sid, path); err != nil {
		t.Fatalf("first ParseTranscript: %v", err)
	}
	if got := countMessages(t, db, sid); got != 1 {
		t.Fatalf("after first parse got %d rows, want 1", got)
	}

	// Append a second line; second parse should add exactly one row, not two.
	appendTranscript(t, path, []map[string]any{
		assistantTextEntry("uuid-a1", sid, "second"),
	})
	if err := ParseTranscript(sid, path); err != nil {
		t.Fatalf("second ParseTranscript: %v", err)
	}
	if got := countMessages(t, db, sid); got != 2 {
		t.Errorf("after second parse got %d rows, want 2 (offset should have skipped line 1)", got)
	}

	// The persisted offset should now equal the file's full size.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	var offset int64
	if err := db.QueryRow(
		"SELECT transcript_offset FROM sessions WHERE session_id=?", sid,
	).Scan(&offset); err != nil {
		t.Fatalf("read offset: %v", err)
	}
	if offset != info.Size() {
		t.Errorf("transcript_offset=%d, want %d (file size after both writes)", offset, info.Size())
	}
}

// TestParseTranscriptFull_ResetsOffsetAndReparses pins ParseTranscriptFull's
// distinction from ParseTranscript: it zeros transcript_offset before
// invoking the parser, so the second pass re-reads the whole file. The
// INSERT OR IGNORE on (message_uuid UNIQUE) means re-imports don't duplicate
// rows, but the offset must visibly reset to 0 and then advance to file size.
func TestParseTranscriptFull_ResetsOffsetAndReparses(t *testing.T) {
	db := withTestDB(t)
	const sid = "sess-full"
	seedTranscriptSession(t, db, sid)

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeTranscript(t, path, []map[string]any{
		userStringEntry("uuid-u1", sid, "one"),
		assistantTextEntry("uuid-a1", sid, "two"),
	})

	if err := ParseTranscript(sid, path); err != nil {
		t.Fatalf("ParseTranscript: %v", err)
	}
	if got := countMessages(t, db, sid); got != 2 {
		t.Fatalf("after first parse got %d rows, want 2", got)
	}

	// Now ParseTranscriptFull: offset reset to 0, then re-parse advances
	// back to file size. INSERT OR IGNORE keeps row count at 2.
	if err := ParseTranscriptFull(sid, path); err != nil {
		t.Fatalf("ParseTranscriptFull: %v", err)
	}
	if got := countMessages(t, db, sid); got != 2 {
		t.Errorf("after full re-parse got %d rows, want 2 (uuid UNIQUE blocks dupes)", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	var offset int64
	if err := db.QueryRow(
		"SELECT transcript_offset FROM sessions WHERE session_id=?", sid,
	).Scan(&offset); err != nil {
		t.Fatalf("read offset: %v", err)
	}
	if offset != info.Size() {
		t.Errorf("post-full offset=%d, want %d", offset, info.Size())
	}
}

// TestParseTranscriptFull_EmptyPathNoOp pins the early return: an empty path
// short-circuits before the offset reset, mirroring ParseTranscript.
func TestParseTranscriptFull_EmptyPathNoOp(t *testing.T) {
	withTestDB(t)
	if err := ParseTranscriptFull("sess-x", ""); err != nil {
		t.Errorf("ParseTranscriptFull with empty path: %v, want nil", err)
	}
}

// TestSetGetTranscriptPath_RoundTrip pins the round-trip: SetTranscriptPath
// stores the path on the sessions row and GetTranscriptPath returns it.
func TestSetGetTranscriptPath_RoundTrip(t *testing.T) {
	db := withTestDB(t)
	const sid = "sess-path"
	seedTranscriptSession(t, db, sid)

	want := "/tmp/claude/transcripts/abc.jsonl"
	if err := SetTranscriptPath(sid, want); err != nil {
		t.Fatalf("SetTranscriptPath: %v", err)
	}
	if got := GetTranscriptPath(sid); got != want {
		t.Errorf("GetTranscriptPath = %q, want %q", got, want)
	}
}

// TestGetTranscriptPath_MissingSessionEmpty pins the no-row branch: an
// unknown session id returns "" with no panic (the QueryRow's error is
// swallowed by design — callers treat "" as "no transcript known").
func TestGetTranscriptPath_MissingSessionEmpty(t *testing.T) {
	withTestDB(t)
	if got := GetTranscriptPath("sess-unknown"); got != "" {
		t.Errorf("GetTranscriptPath on missing row = %q, want \"\"", got)
	}
}

// TestFlagNeedsRecap_SetsColumnAtThreshold pins the documented behavior:
// once a session has accumulated >= 10 user messages beyond its
// summary_seq, FlagNeedsRecap flips needs_recap = 1.
func TestFlagNeedsRecap_SetsColumnAtThreshold(t *testing.T) {
	db := withTestDB(t)
	const sid = "sess-recap"
	seedTranscriptSession(t, db, sid)

	// Seed 10 user session_messages.
	for i := 0; i < 10; i++ {
		if _, err := db.Exec(
			`INSERT INTO session_messages
			 (session_id, role, content, message_uuid, created_at)
			 VALUES (?, 'user', 'hi', ?, '2026-05-29T00:00:00')`,
			sid, "uuid-recap-"+itoa(int64(i)),
		); err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
	}

	FlagNeedsRecap(sid)

	var flag int
	if err := db.QueryRow(
		"SELECT needs_recap FROM sessions WHERE session_id=?", sid,
	).Scan(&flag); err != nil {
		t.Fatalf("read needs_recap: %v", err)
	}
	if flag != 1 {
		t.Errorf("needs_recap = %d after 10 user messages, want 1", flag)
	}
}

// TestFlagNeedsRecap_BelowThresholdNoFlag pins the inverse: with fewer
// than 10 new user messages since summary_seq, needs_recap stays 0.
func TestFlagNeedsRecap_BelowThresholdNoFlag(t *testing.T) {
	db := withTestDB(t)
	const sid = "sess-norecap"
	seedTranscriptSession(t, db, sid)

	for i := 0; i < 5; i++ {
		if _, err := db.Exec(
			`INSERT INTO session_messages
			 (session_id, role, content, message_uuid, created_at)
			 VALUES (?, 'user', 'hi', ?, '2026-05-29T00:00:00')`,
			sid, "uuid-norecap-"+itoa(int64(i)),
		); err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
	}

	FlagNeedsRecap(sid)

	var flag int
	if err := db.QueryRow(
		"SELECT needs_recap FROM sessions WHERE session_id=?", sid,
	).Scan(&flag); err != nil {
		t.Fatalf("read needs_recap: %v", err)
	}
	if flag != 0 {
		t.Errorf("needs_recap = %d after 5 user messages, want 0", flag)
	}
}
