package hookcmd

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// TestRevisitClearVerbRe pins the early-return matcher: the user's own
// gate-clearing commands (and only those) must never be blocked by the gate.
func TestRevisitClearVerbRe(t *testing.T) {
	cases := []struct {
		name  string
		cmd   string
		match bool
	}{
		{"continue plain", "endless task continue", true},
		{"pause plain", "endless task pause", true},
		{"wrapper prefix", "uv run endless task continue", true},
		{"absolute path prefix", "/usr/local/bin/endless task pause", true},
		{"extra whitespace", "endless   task    continue", true},

		{"unrelated task verb", "endless task claim E-5", false},
		{"git continue", "git rebase --continue", false},
		{"not endless", "task continue", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := revisitClearVerbRe.MatchString(c.cmd); got != c.match {
				t.Errorf("MatchString(%q) = %v, want %v", c.cmd, got, c.match)
			}
		})
	}
}

// TestRevisitPromptInstruction confirms the instruction names both tasks and
// routes Claude to an AskUserQuestion with the two clearing verbs.
func TestRevisitPromptInstruction(t *testing.T) {
	msg := revisitPromptInstruction(42, 7)
	for _, want := range []string{
		"E-42", "E-7", "revisit", "AskUserQuestion",
		"endless task continue", "endless task pause",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("instruction missing %q:\n%s", want, msg)
		}
	}
}

// TestRevisitBlockResponse_Shape pins the structural contract of the JSON block
// response (E-1542 §5): decision "block" plus hookSpecificOutput carrying the
// PreToolUse event name and the instruction as additionalContext. This is the
// shape the live-Claude verification (and the verify script) checks.
func TestRevisitBlockResponse_Shape(t *testing.T) {
	b, err := json.Marshal(revisitBlockResponse("DO THE THING"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["decision"] != "block" {
		t.Errorf("decision = %v, want block", got["decision"])
	}
	if got["reason"] != "DO THE THING" {
		t.Errorf("reason = %v, want instruction text", got["reason"])
	}
	hso, ok := got["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("hookSpecificOutput missing/wrong type: %v", got["hookSpecificOutput"])
	}
	if hso["hookEventName"] != "PreToolUse" {
		t.Errorf("hookEventName = %v, want PreToolUse", hso["hookEventName"])
	}
	if hso["additionalContext"] != "DO THE THING" {
		t.Errorf("additionalContext = %v, want instruction text", hso["additionalContext"])
	}
}

// TestRevisitGateDecision_Lifecycle drives the DB-backed decision through its
// full lifecycle against a seeded DB: first-call block + gate set, repeat block,
// the clear-verb bypass, epic-leaves-revisit auto-clear, and no-active-task skip.
func TestRevisitGateDecision_Lifecycle(t *testing.T) {
	db := newSchemaDB(t)
	restore := monitor.SetTestDB(db)
	t.Cleanup(restore)

	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/p')")
	exec("INSERT INTO tasks (id, project_id, title, status, type_id) VALUES (10, 1, 'epic', 'revisit', 4)")
	exec("INSERT INTO tasks (id, project_id, parent_id, title, status, type_id) VALUES (11, 1, 10, 'child', 'ready', 1)")
	exec(`INSERT INTO sessions (id, session_id, project_id, platform, state, active_task_id, started_at, last_activity)
	      VALUES (1, 'sess-1', 1, 'claude', 'working', 11, '2026-06-20T00:00:00', '2026-06-20T00:00:00')`)

	read := claudePayload{EventName: "PreToolUse", ToolName: "Read", SessionID: "sess-1"}

	// 1. First tool call blocks, opens a gate, instruction names epic + child.
	instr, block := revisitGateDecision(read)
	if !block {
		t.Fatalf("first call: block=false, want true")
	}
	if !strings.Contains(instr, "E-10") || !strings.Contains(instr, "E-11") {
		t.Errorf("instruction = %q, want E-10 + E-11", instr)
	}
	if _, found, _ := monitor.PendingRevisitGate(1); !found {
		t.Errorf("no open gate after first block")
	}

	// 2. Repeat call still blocks (gate pending, epic still revisit).
	if _, block := revisitGateDecision(read); !block {
		t.Errorf("repeat call: block=false, want true (gate still pending)")
	}

	// 3. The user's clearing command is never blocked.
	clear := claudePayload{
		EventName: "PreToolUse", ToolName: "Bash", SessionID: "sess-1",
		ToolInput: json.RawMessage(`{"command":"endless task continue"}`),
	}
	if _, block := revisitGateDecision(clear); block {
		t.Errorf("`endless task continue` was blocked, want allowed")
	}

	// 4. Epic leaves revisit -> next call auto-clears (revisit_resolved) + allows.
	exec("UPDATE tasks SET status='underway' WHERE id=10")
	if _, block := revisitGateDecision(read); block {
		t.Errorf("after epic left revisit: block=true, want false (auto-clear)")
	}
	var by string
	if err := db.QueryRow(
		"SELECT cleared_by FROM session_gates WHERE session_id=1 ORDER BY id DESC LIMIT 1",
	).Scan(&by); err != nil {
		t.Fatalf("read cleared_by: %v", err)
	}
	if by != "revisit_resolved" {
		t.Errorf("cleared_by = %q, want revisit_resolved", by)
	}

	// 5. No active task -> never blocks.
	exec("UPDATE sessions SET active_task_id=NULL WHERE id=1")
	if _, block := revisitGateDecision(read); block {
		t.Errorf("session with no active task: block=true, want false")
	}
}

// newSchemaDB opens a fresh file-backed SQLite DB with schema.SQL applied.
func newSchemaDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "endless.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}
