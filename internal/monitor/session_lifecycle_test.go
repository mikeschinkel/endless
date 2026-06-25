package monitor

import (
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikeschinkel/go-cfgstore"
)

// init wires cfgstore's package-global logger so GetTrackingMode tests
// that exercise config.Load don't panic in cfgstore.EnsureLogger. The
// production binaries set this in their own startup; tests have no such
// entry point, so we do it here. A discard handler is sufficient — the
// tests assert on return values, not log output.
func init() {
	cfgstore.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// sessionLifecycleRow reads the lifecycle-relevant columns for a session.
// Distinct from sessionRow (session_test.go) — that helper covers the
// touch-shape (state/process/platform); this one targets the columns the
// task-bound lifecycle helpers mutate.
func sessionLifecycleRow(t *testing.T, db *sql.DB, sessionID string) (state string, activeTaskID *int64, process string) {
	t.Helper()
	err := db.QueryRow(
		"SELECT state, active_task_id, COALESCE(process, '') FROM sessions WHERE session_id=?",
		sessionID,
	).Scan(&state, &activeTaskID, &process)
	if err != nil {
		t.Fatalf("read session %q: %v", sessionID, err)
	}
	return
}

// seedTask inserts a tasks row with the given id, project_id, status, and
// title (NOT NULL per schema). Returns the id for chained calls.
func seedTask(t *testing.T, db *sql.DB, id, projectID int64, title, status string) int64 {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		id, projectID, title, status,
	); err != nil {
		t.Fatalf("seed task id=%d: %v", id, err)
	}
	return id
}

// taskStatus returns the status column for taskID.
func taskStatus(t *testing.T, db *sql.DB, taskID int64) string {
	t.Helper()
	var s string
	if err := db.QueryRow("SELECT status FROM tasks WHERE id=?", taskID).Scan(&s); err != nil {
		t.Fatalf("read task %d status: %v", taskID, err)
	}
	return s
}

// TestBindSessionToTask_InsertCreatesWorking pins the INSERT branch: a
// first bind for an unknown session creates the row with state='working',
// points active_task_id at taskID, and captures TMUX_PANE into process.
func TestBindSessionToTask_InsertCreatesWorking(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "ready")
	t.Setenv("TMUX_PANE", "%5")

	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("BindSessionToTask: %v", err)
	}
	state, activeTaskID, process := sessionLifecycleRow(t, db, "sess-A")
	if state != "working" {
		t.Errorf("state = %q, want working", state)
	}
	if activeTaskID == nil || *activeTaskID != 42 {
		t.Errorf("active_task_id = %v, want 42", activeTaskID)
	}
	if process != "%5" {
		t.Errorf("process = %q, want %%5", process)
	}
}

// TestBindSessionToTask_UpsertOverridesPriorState pins the UPDATE branch:
// re-binding an existing session forces state back to 'working' and
// repoints active_task_id, even if the prior row was in another state.
func TestBindSessionToTask_UpsertOverridesPriorState(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 100, 1, "first task", "ready")
	seedTask(t, db, 200, 1, "second task", "ready")
	t.Setenv("TMUX_PANE", "%5")

	if err := BindSessionToTask("sess-A", 1, 100); err != nil {
		t.Fatalf("bind 1: %v", err)
	}
	// Force the session into a non-working state and clear pane to
	// confirm the next bind re-establishes both.
	if _, err := db.Exec(
		"UPDATE sessions SET state='idle' WHERE session_id=?", "sess-A",
	); err != nil {
		t.Fatalf("force idle: %v", err)
	}
	if err := BindSessionToTask("sess-A", 1, 200); err != nil {
		t.Fatalf("bind 2: %v", err)
	}
	state, activeTaskID, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "working" {
		t.Errorf("state = %q, want working (re-bind didn't lift idle)", state)
	}
	if activeTaskID == nil || *activeTaskID != 200 {
		t.Errorf("active_task_id = %v, want 200", activeTaskID)
	}
}

// TestBindSessionToTask_EmptyPaneDoesNotStompProcess pins the
// COALESCE-on-update behaviour: an empty TMUX_PANE during a re-bind must
// not erase a previously-captured process value.
func TestBindSessionToTask_EmptyPaneDoesNotStompProcess(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "ready")

	t.Setenv("TMUX_PANE", "%5")
	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("bind 1: %v", err)
	}
	t.Setenv("TMUX_PANE", "")
	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("bind 2: %v", err)
	}
	_, _, process := sessionLifecycleRow(t, db, "sess-A")
	if process != "%5" {
		t.Errorf("process = %q, want %%5 (empty pane stomped known value)", process)
	}
}

// TestStartWorkSession_PromotesEligibleStatus pins the underway
// transition: tasks in unplanned/ready/blocked flip to underway as
// part of the defense-in-depth mirror of claim_item events.
func TestStartWorkSession_PromotesEligibleStatus(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	cases := []struct {
		taskID int64
		status string
	}{
		{1, "unplanned"},
		{2, "ready"},
		{3, "blocked"},
	}
	for _, c := range cases {
		seedTask(t, db, c.taskID, 1, "task", c.status)
	}
	t.Setenv("TMUX_PANE", "%5")

	for _, c := range cases {
		sid := "sess-" + c.status
		if err := StartWorkSession(sid, 1, c.taskID); err != nil {
			t.Fatalf("StartWorkSession(%s): %v", c.status, err)
		}
		if got := taskStatus(t, db, c.taskID); got != "underway" {
			t.Errorf("from %s: task status = %q, want underway", c.status, got)
		}
	}
}

// TestStartWorkSession_DoesNotDemoteIneligibleStatus pins the WHERE
// clause: tasks in statuses outside unplanned/ready/blocked (e.g.
// underway, confirmed) are left alone — the helper must not stomp a
// task already past the entry gates.
func TestStartWorkSession_DoesNotDemoteIneligibleStatus(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 50, 1, "already in progress", "underway")
	seedTask(t, db, 60, 1, "already confirmed", "confirmed")
	t.Setenv("TMUX_PANE", "%5")

	if err := StartWorkSession("sess-A", 1, 50); err != nil {
		t.Fatalf("StartWorkSession 50: %v", err)
	}
	if got := taskStatus(t, db, 50); got != "underway" {
		t.Errorf("underway task became %q", got)
	}
	if err := StartWorkSession("sess-B", 1, 60); err != nil {
		t.Fatalf("StartWorkSession 60: %v", err)
	}
	if got := taskStatus(t, db, 60); got != "confirmed" {
		t.Errorf("confirmed task became %q (should be untouched)", got)
	}
}

// TestStartChatSession_InsertWithNullTask pins the chat-only shape: a
// fresh session lands in working state with active_task_id=NULL.
func TestStartChatSession_InsertWithNullTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	t.Setenv("TMUX_PANE", "%5")

	if err := StartChatSession("sess-A", 1); err != nil {
		t.Fatalf("StartChatSession: %v", err)
	}
	state, activeTaskID, process := sessionLifecycleRow(t, db, "sess-A")
	if state != "working" {
		t.Errorf("state = %q, want working", state)
	}
	if activeTaskID != nil {
		t.Errorf("active_task_id = %v, want NULL", *activeTaskID)
	}
	if process != "%5" {
		t.Errorf("process = %q, want %%5", process)
	}
}

// TestStartChatSession_UpsertClearsActiveTask pins the documented
// chat-takeover semantics: starting a chat on a session that was
// previously bound to a task drops the active_task_id back to NULL.
func TestStartChatSession_UpsertClearsActiveTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "ready")
	t.Setenv("TMUX_PANE", "%5")

	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := StartChatSession("sess-A", 1); err != nil {
		t.Fatalf("StartChatSession: %v", err)
	}
	state, activeTaskID, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "working" {
		t.Errorf("state = %q, want working", state)
	}
	if activeTaskID != nil {
		t.Errorf("active_task_id = %v, want NULL (chat takeover didn't clear)", *activeTaskID)
	}
}

// TestInitSession_InsertCreatesNeedsInput pins the SessionStart shape:
// first call creates the row in state='needs_input'.
func TestInitSession_InsertCreatesNeedsInput(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := InitSession("sess-A", 1); err != nil {
		t.Fatalf("InitSession: %v", err)
	}
	state, _, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "needs_input" {
		t.Errorf("state = %q, want needs_input", state)
	}
}

// TestInitSession_UpsertPreservesState pins the second-call contract:
// when the session row already exists in a non-default state (e.g.
// working), InitSession only refreshes last_activity and must not
// regress the lifecycle state.
func TestInitSession_UpsertPreservesState(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := InitSession("sess-A", 1); err != nil {
		t.Fatalf("init 1: %v", err)
	}
	if _, err := db.Exec(
		"UPDATE sessions SET state='working' WHERE session_id=?", "sess-A",
	); err != nil {
		t.Fatalf("force working: %v", err)
	}
	if err := InitSession("sess-A", 1); err != nil {
		t.Fatalf("init 2: %v", err)
	}
	state, _, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "working" {
		t.Errorf("state = %q, want working (InitSession regressed it)", state)
	}
}

// TestGetActiveSession_ReturnsRow pins the happy path: an existing
// session is returned with the fields the lifecycle helpers populated.
func TestGetActiveSession_ReturnsRow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "ready")
	t.Setenv("TMUX_PANE", "%5")
	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("bind: %v", err)
	}

	s, err := GetActiveSession("sess-A")
	if err != nil {
		t.Fatalf("GetActiveSession: %v", err)
	}
	if s.SessionID != "sess-A" {
		t.Errorf("SessionID = %q, want sess-A", s.SessionID)
	}
	if s.ProjectID != 1 {
		t.Errorf("ProjectID = %d, want 1", s.ProjectID)
	}
	if s.State != "working" {
		t.Errorf("State = %q, want working", s.State)
	}
	if s.ActiveTaskID == nil || *s.ActiveTaskID != 42 {
		t.Errorf("ActiveTaskID = %v, want 42", s.ActiveTaskID)
	}
}

// TestGetActiveSession_MissingReturnsError pins the unknown-session
// branch: callers must observe an error so they can distinguish absent
// from present-but-cleared rows.
func TestGetActiveSession_MissingReturnsError(t *testing.T) {
	withTestDB(t)
	s, err := GetActiveSession("nope")
	if err == nil {
		t.Fatalf("GetActiveSession(missing) returned (%+v, nil), want error", s)
	}
}

// TestPlanFilePath_RoundTrip pins the SetPlanFilePath / GetPlanFilePath
// pair: the value written is the value read.
func TestPlanFilePath_RoundTrip(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if err := InitSession("sess-A", 1); err != nil {
		t.Fatalf("init: %v", err)
	}

	want := "/tmp/plan.md"
	if err := SetPlanFilePath("sess-A", want); err != nil {
		t.Fatalf("SetPlanFilePath: %v", err)
	}
	if got := GetPlanFilePath("sess-A"); got != want {
		t.Errorf("GetPlanFilePath = %q, want %q", got, want)
	}
}

// TestGetPlanFilePath_UnsetReturnsEmpty pins the documented contract:
// an unset (NULL) plan_file_path is reported as "" so callers don't
// need to distinguish absent from empty.
func TestGetPlanFilePath_UnsetReturnsEmpty(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if err := InitSession("sess-A", 1); err != nil {
		t.Fatalf("init: %v", err)
	}

	if got := GetPlanFilePath("sess-A"); got != "" {
		t.Errorf("unset GetPlanFilePath = %q, want \"\"", got)
	}
}

// TestGetPlanFilePath_MissingSessionReturnsEmpty pins the swallowed-
// error branch: unknown sessions return "" rather than surfacing the
// sql.ErrNoRows up the stack.
func TestGetPlanFilePath_MissingSessionReturnsEmpty(t *testing.T) {
	withTestDB(t)
	if got := GetPlanFilePath("missing"); got != "" {
		t.Errorf("missing GetPlanFilePath = %q, want \"\"", got)
	}
}

// TestCompleteTask_FlipsTaskAndClearsActive pins the two-step write:
// the task moves to 'confirmed' and the session's active_task_id is
// cleared back to NULL with state='idle'.
func TestCompleteTask_FlipsTaskAndClearsActive(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "ready")
	t.Setenv("TMUX_PANE", "%5")
	if err := StartWorkSession("sess-A", 1, 42); err != nil {
		t.Fatalf("StartWorkSession: %v", err)
	}

	if err := CompleteTask("sess-A", 42); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	if got := taskStatus(t, db, 42); got != "confirmed" {
		t.Errorf("task status = %q, want confirmed", got)
	}
	state, activeTaskID, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "idle" {
		t.Errorf("session state = %q, want idle", state)
	}
	if activeTaskID != nil {
		t.Errorf("active_task_id = %v, want NULL", *activeTaskID)
	}
}

// TestCompleteTask_RecordsCompletedAt pins the audit trail: confirming
// a task stamps completed_at, used downstream by ledgers and reporting.
func TestCompleteTask_RecordsCompletedAt(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "ready")
	t.Setenv("TMUX_PANE", "%5")
	if err := StartWorkSession("sess-A", 1, 42); err != nil {
		t.Fatalf("StartWorkSession: %v", err)
	}

	if err := CompleteTask("sess-A", 42); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	var completedAt sql.NullString
	if err := db.QueryRow("SELECT completed_at FROM tasks WHERE id=?", 42).Scan(&completedAt); err != nil {
		t.Fatalf("read completed_at: %v", err)
	}
	if !completedAt.Valid || completedAt.String == "" {
		t.Errorf("completed_at = %v, want non-empty timestamp", completedAt)
	}
}

// TestIdleSession_FlipsState pins the between-turns transition: a live
// session moves to 'idle' without disturbing other columns.
func TestIdleSession_FlipsState(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "ready")
	t.Setenv("TMUX_PANE", "%5")
	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("bind: %v", err)
	}

	if err := IdleSession("sess-A"); err != nil {
		t.Fatalf("IdleSession: %v", err)
	}
	state, activeTaskID, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "idle" {
		t.Errorf("state = %q, want idle", state)
	}
	// active_task_id is intentionally preserved across idle — only
	// CompleteTask clears it. Pin that here so a future change has to
	// justify breaking the contract.
	if activeTaskID == nil || *activeTaskID != 42 {
		t.Errorf("active_task_id = %v, want 42 (idle should not clear)", activeTaskID)
	}
}

// TestEndSession_FlipsState pins the terminal transition: an active
// session moves to 'ended'. Pane-collision invalidation in TouchSession
// uses state='ended' as the inactive marker, so this transition matters
// for the cross-session invariant too.
func TestEndSession_FlipsState(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if err := InitSession("sess-A", 1); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := EndSession("sess-A"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	state, _, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "ended" {
		t.Errorf("state = %q, want ended", state)
	}
}

// TestIsSessionExpired_TableDriven covers the pure timestamp branches:
// recent activity is not expired; old activity is; empty and malformed
// timestamps are treated as expired (caller can't tell when the session
// last spoke, so it must be considered dead).
func TestIsSessionExpired_TableDriven(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name           string
		lastActivity   string
		timeoutMinutes int
		want           bool
	}{
		{
			name:           "recent activity not expired",
			lastActivity:   now.Add(-1 * time.Minute).Format("2006-01-02T15:04:05"),
			timeoutMinutes: 30,
			want:           false,
		},
		{
			name:           "stale activity expired",
			lastActivity:   now.Add(-2 * time.Hour).Format("2006-01-02T15:04:05"),
			timeoutMinutes: 30,
			want:           true,
		},
		{
			name:           "empty timestamp expired",
			lastActivity:   "",
			timeoutMinutes: 30,
			want:           true,
		},
		{
			name:           "malformed timestamp expired",
			lastActivity:   "not-a-time",
			timeoutMinutes: 30,
			want:           true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &SessionInfo{LastActivity: c.lastActivity}
			if got := IsSessionExpired(s, c.timeoutMinutes); got != c.want {
				t.Errorf("IsSessionExpired(%q, %d) = %v, want %v",
					c.lastActivity, c.timeoutMinutes, got, c.want)
			}
		})
	}
}

// TestGetTrackingMode_AnonymousReturnsOff pins the short-circuit:
// projects with status='anonymous' bypass config entirely and report
// 'off' so transient/scratch projects don't trip enforcement.
func TestGetTrackingMode_AnonymousReturnsOff(t *testing.T) {
	db := withTestDB(t)
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path, status) VALUES (?, ?, ?, ?)",
		1, "anon-proj", "/tmp/anon", "anonymous",
	); err != nil {
		t.Fatalf("seed anon project: %v", err)
	}

	if got := GetTrackingMode(1); got != "off" {
		t.Errorf("GetTrackingMode(anonymous) = %q, want off", got)
	}
}

// TestGetTrackingMode_MissingProjectReturnsOff pins the unknown-id
// branch: a project id with no row returns 'off' rather than surfacing
// the sql error to the caller.
func TestGetTrackingMode_MissingProjectReturnsOff(t *testing.T) {
	withTestDB(t)
	if got := GetTrackingMode(99999); got != "off" {
		t.Errorf("GetTrackingMode(missing) = %q, want off", got)
	}
}

// TestGetTrackingMode_DefaultsToEnforce pins the registered-project
// default: a registered project with no .endless/config.json gets
// 'enforce' so the gate fires unless explicitly opted out.
func TestGetTrackingMode_DefaultsToEnforce(t *testing.T) {
	db := withTestDB(t)
	// Redirect HOME so the CLI config layer can't pick up real-user
	// values from ~/.config/endless/config.json on the test machine.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	projectPath := t.TempDir()
	seedProject(t, db, 1, "registered-proj", projectPath)

	if got := GetTrackingMode(1); got != "enforce" {
		t.Errorf("GetTrackingMode(default) = %q, want enforce", got)
	}
}

// TestGetTrackingMode_ConfigTrackPassesThrough pins the override path:
// a project-layer config.json with tracking="track" lands as "track"
// (not the default "enforce").
func TestGetTrackingMode_ConfigTrackPassesThrough(t *testing.T) {
	db := withTestDB(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	projectPath := t.TempDir()
	endlessDir := filepath.Join(projectPath, ".endless")
	if err := os.MkdirAll(endlessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := `{"tracking": "track"}`
	if err := os.WriteFile(filepath.Join(endlessDir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	seedProject(t, db, 1, "registered-proj", projectPath)

	if got := GetTrackingMode(1); got != "track" {
		t.Errorf("GetTrackingMode(track-cfg) = %q, want track", got)
	}
}

// TestGetTrackingMode_ConfigOffPassesThrough mirrors the track case for
// the explicit opt-out: tracking="off" suppresses enforcement.
func TestGetTrackingMode_ConfigOffPassesThrough(t *testing.T) {
	db := withTestDB(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	projectPath := t.TempDir()
	endlessDir := filepath.Join(projectPath, ".endless")
	if err := os.MkdirAll(endlessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := `{"tracking": "off"}`
	if err := os.WriteFile(filepath.Join(endlessDir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	seedProject(t, db, 1, "registered-proj", projectPath)

	if got := GetTrackingMode(1); got != "off" {
		t.Errorf("GetTrackingMode(off-cfg) = %q, want off", got)
	}
}
