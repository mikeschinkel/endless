package events

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- Sentinel session-id resolution (E-1588) ------------------------------

// sessionStatusEvent builds a session_status.recorded Event whose payload's
// process field is `process` (e.g. "__session_id=42" or a tmux pane).
func sessionStatusEvent(t *testing.T, process string) *Event {
	t.Helper()
	payload, err := json.Marshal(SessionStatusRecordedPayload{
		Process:  process,
		Headline: "probe",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-06-17T00:00:00",
		Kind:    KindSessionStatusRecorded,
		Project: "test",
		Entity:  EntityRef{Type: "session_status", ID: "0"},
		Payload: payload,
	}
}

func TestExecSessionStatus_SentinelUsesIDDirectly(t *testing.T) {
	db := newLandingTestDB(t)
	// A live session whose process does NOT match what we'll send — the
	// sentinel path must resolve by id, not by pane.
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, process, started_at)
		 VALUES (42, 'sess-42', 1, 'working', '%99', '2026-06-17T00:00:00')`,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	res, err := execSessionStatusRecorded(db, sessionStatusEvent(t, "__session_id=42"))
	if err != nil {
		t.Fatalf("execSessionStatusRecorded: %v", err)
	}
	if res.SessionStatusID == 0 {
		t.Fatalf("expected an inserted row, got %+v", res)
	}

	var sessionID int64
	if err := db.QueryRow(
		`SELECT session_id FROM session_statuses WHERE id = ?`,
		res.SessionStatusID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("read inserted row: %v", err)
	}
	if sessionID != 42 {
		t.Errorf("session_id = %d, want 42", sessionID)
	}
}

func TestExecSessionStatus_SentinelNonexistentIDErrors(t *testing.T) {
	db := newLandingTestDB(t)
	_, err := execSessionStatusRecorded(db, sessionStatusEvent(t, "__session_id=999"))
	if err == nil {
		t.Fatal("expected error for non-existent session id, got nil")
	}
	if !strings.Contains(err.Error(), "not a live session") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecSessionStatus_SentinelEndedIDErrors(t *testing.T) {
	db := newLandingTestDB(t)
	// state='ended' rows are not live; the sessions_null_process trigger
	// nulls process at end-of-life, so seed with NULL process.
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, started_at)
		 VALUES (7, 'sess-7', 1, 'ended', '2026-06-17T00:00:00')`,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	_, err := execSessionStatusRecorded(db, sessionStatusEvent(t, "__session_id=7"))
	if err == nil {
		t.Fatal("expected error for ended session id, got nil")
	}
	if !strings.Contains(err.Error(), "not a live session") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecSessionStatus_NoSentinelResolvesByPane(t *testing.T) {
	db := newLandingTestDB(t)
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, process, started_at)
		 VALUES (5, 'sess-5', 1, 'working', '%88', '2026-06-17T00:00:00')`,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	res, err := execSessionStatusRecorded(db, sessionStatusEvent(t, "%88"))
	if err != nil {
		t.Fatalf("execSessionStatusRecorded: %v", err)
	}
	var sessionID int64
	if err := db.QueryRow(
		`SELECT session_id FROM session_statuses WHERE id = ?`,
		res.SessionStatusID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("read inserted row: %v", err)
	}
	if sessionID != 5 {
		t.Errorf("session_id = %d, want 5 (resolved by pane)", sessionID)
	}
}

func TestRenderSessionStatusMarkdown_Headline(t *testing.T) {
	p := &SessionStatusRecordedPayload{Headline: "E-1312 v1 landed."}
	md := renderSessionStatusMarkdown(p)
	if !strings.Contains(md, "## Status\nE-1312 v1 landed.") {
		t.Fatalf("missing Status section: %q", md)
	}
}

func TestRenderSessionStatusMarkdown_EmptySectionsShowEmpty(t *testing.T) {
	p := &SessionStatusRecordedPayload{}
	md := renderSessionStatusMarkdown(p)
	for _, want := range []string{
		"## Resolved\n(empty)",
		"## Pending\n(empty)",
		"## Blocked\n(empty)",
		"## Unverified\n(empty)",
		"## Decisions\n(empty)",
		"## Commits\n(empty)",
		"## Memory\n(empty)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing section/empty marker %q in:\n%s", want, md)
		}
	}
}

func TestRenderSessionStatusMarkdown_TaskTable(t *testing.T) {
	p := &SessionStatusRecordedPayload{
		Tasks:`<task id="E-1208" status="confirmed">verbs.jsonl write-time</task>` +
			"\n" +
			`<task id="E-1206" status="confirmed" filed="true">db-ledger write-time</task>`,
	}
	md := renderSessionStatusMarkdown(p)
	if !strings.Contains(md, "| Task | Status | Note |") {
		t.Fatalf("missing table header: %q", md)
	}
	if !strings.Contains(md, "| E-1208 | confirmed | verbs.jsonl write-time |") {
		t.Errorf("missing first task row: %q", md)
	}
	if !strings.Contains(md, "| E-1206 (filed) | confirmed | db-ledger write-time |") {
		t.Errorf("missing filed-marked second task row: %q", md)
	}
}

func TestRenderSessionStatusMarkdown_MultiLineNote(t *testing.T) {
	// Multi-line task body: newlines should render as <br> in the markdown
	// table cell.
	p := &SessionStatusRecordedPayload{
		Tasks:`<task id="E-1" status="confirmed">line one
line two</task>`,
	}
	md := renderSessionStatusMarkdown(p)
	if !strings.Contains(md, "line one<br>line two") {
		t.Fatalf("expected <br> between lines in cell: %q", md)
	}
}

func TestRenderSessionStatusMarkdown_DecisionsBulleted(t *testing.T) {
	p := &SessionStatusRecordedPayload{
		Decisions: `<decision>chose XML over markdown</decision>` +
			"\n" +
			`<decision>kept filed as attribute</decision>`,
	}
	md := renderSessionStatusMarkdown(p)
	if !strings.Contains(md, "- chose XML over markdown") {
		t.Errorf("missing first bullet: %q", md)
	}
	if !strings.Contains(md, "- kept filed as attribute") {
		t.Errorf("missing second bullet: %q", md)
	}
}

func TestRenderSessionStatusMarkdown_CommitsTable(t *testing.T) {
	p := &SessionStatusRecordedPayload{
		Commits: `<commit sha="1e3bbfc">ledger split 1264 to 500/500/264</commit>`,
	}
	md := renderSessionStatusMarkdown(p)
	if !strings.Contains(md, "| SHA | Description |") {
		t.Errorf("missing commits table header: %q", md)
	}
	if !strings.Contains(md, "| 1e3bbfc | ledger split 1264 to 500/500/264 |") {
		t.Errorf("missing commit row: %q", md)
	}
}

func TestRenderSessionStatusMarkdown_MemoryTable(t *testing.T) {
	p := &SessionStatusRecordedPayload{
		Memory: `<entry path="feedback_no_autonomous_remediation.md">report and ask on partial fail</entry>`,
	}
	md := renderSessionStatusMarkdown(p)
	if !strings.Contains(md, "| Path | Summary |") {
		t.Errorf("missing memory table header: %q", md)
	}
	if !strings.Contains(md, "| feedback_no_autonomous_remediation.md | report and ask on partial fail |") {
		t.Errorf("missing memory row: %q", md)
	}
}

// --- Helpers --------------------------------------------------------------

func TestExtractAttr(t *testing.T) {
	cases := []struct {
		line, attr, want string
	}{
		{`<task id="E-1" status="confirmed">x</task>`, "id", "E-1"},
		{`<task id="E-1" status="confirmed">x</task>`, "status", "confirmed"},
		{`<task id="E-1" status="confirmed">x</task>`, "filed", ""},
		{`<commit sha="1e3bbfc">x</commit>`, "sha", "1e3bbfc"},
		{`<entry path="a/b/c.md">x</entry>`, "path", "a/b/c.md"},
	}
	for _, c := range cases {
		if got := extractAttr(c.line, c.attr); got != c.want {
			t.Errorf("extractAttr(%q, %q) = %q, want %q", c.line, c.attr, got, c.want)
		}
	}
}

func TestExtractElementText(t *testing.T) {
	cases := []struct {
		line, tag, want string
	}{
		{`<task id="E-1" status="confirmed">body text</task>`, "task", "body text"},
		{`<decision>chose XML</decision>`, "decision", "chose XML"},
		{`<commit sha="abc">desc</commit>`, "commit", "desc"},
		{`<task id="E-1" status="confirmed">multi
line</task>`, "task", "multi\nline"},
	}
	for _, c := range cases {
		if got := extractElementText(c.line, c.tag); got != c.want {
			t.Errorf("extractElementText(%q, %q) = %q, want %q",
				c.line, c.tag, got, c.want)
		}
	}
}

func TestNullableEq(t *testing.T) {
	empty := ""
	hello := "hello"
	cases := []struct {
		col     *string
		payload string
		want    bool
	}{
		{nil, "", true},         // both effectively empty
		{nil, "hello", false},   // null col vs non-empty payload
		{&empty, "", true},      // empty col vs empty payload
		{&hello, "hello", true}, // matching strings
		{&hello, "world", false},
	}
	for _, c := range cases {
		got := nullableEq(c.col, c.payload)
		if got != c.want {
			t.Errorf("nullableEq(%v, %q) = %v, want %v", c.col, c.payload, got, c.want)
		}
	}
}
