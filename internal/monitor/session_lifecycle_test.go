package monitor

import (
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/go-cfgstore"

	"github.com/mikeschinkel/endless/internal/sessionstate"
	"github.com/mikeschinkel/endless/internal/taskstatus"
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
// task-bound lifecycle helpers mutate. `process` is now the pane ADDRESS read
// back through sessions.process_id -> processes (E-1898), so these assertions
// still read as "which pane is this session on".
func sessionLifecycleRow(t *testing.T, db *sql.DB, sessionID string) (state string, taskID *int64, process string) {
	t.Helper()
	err := db.QueryRow(
		`SELECT s.state, s.task_id, COALESCE(p.address, '')
		 FROM sessions s LEFT JOIN processes p ON p.id = s.process_id
		 WHERE s.session_id=?`,
		sessionID,
	).Scan(&state, &taskID, &process)
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
// points task_id at taskID, and captures TMUX_PANE into process.
func TestBindSessionToTask_InsertCreatesWorking(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "ready")
	t.Setenv("TMUX_PANE", "%5")

	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("BindSessionToTask: %v", err)
	}
	state, taskID, process := sessionLifecycleRow(t, db, "sess-A")
	if state != "working" {
		t.Errorf("state = %q, want working", state)
	}
	if taskID == nil || *taskID != 42 {
		t.Errorf("task_id = %v, want 42", taskID)
	}
	if process != "%5" {
		t.Errorf("process = %q, want %%5", process)
	}
}

// TestBindSessionToTask_UpsertOverridesPriorState pins the UPDATE branch:
// re-binding an existing session forces state back to 'working', even if the
// prior row was in another state.
//
// The re-bind names the SAME task it already holds. It used to repoint to a
// second one, which E-1969's write-once trigger now aborts — see
// TestBindSessionToTask_RefusesRepoint below, which pins that refusal. State
// revival and task repointing were never the same behavior; only the trigger
// made that visible.
func TestBindSessionToTask_UpsertOverridesPriorState(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 100, 1, "first task", "ready")
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
	if err := BindSessionToTask("sess-A", 1, 100); err != nil {
		t.Fatalf("bind 2: %v", err)
	}
	state, taskID, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "working" {
		t.Errorf("state = %q, want working (re-bind didn't lift idle)", state)
	}
	if taskID == nil || *taskID != 100 {
		t.Errorf("task_id = %v, want 100", taskID)
	}
}

// TestBindSessionToTask_RefusesRepoint pins ED-1560 at the lowest write path:
// sessions.task_id is write-once, so binding an already-bound session to a
// DIFFERENT task fails and leaves the original binding intact. This is the write
// nobody enumerated — the Python verb refuses first (E-1968), but the trigger is
// what catches a caller that does not.
func TestBindSessionToTask_RefusesRepoint(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 100, 1, "first task", "ready")
	seedTask(t, db, 200, 1, "second task", "ready")
	t.Setenv("TMUX_PANE", "%5")

	if err := BindSessionToTask("sess-A", 1, 100); err != nil {
		t.Fatalf("bind 1: %v", err)
	}
	err := BindSessionToTask("sess-A", 1, 200)
	if err == nil {
		t.Fatal("re-bind to a different task succeeded, want write-once abort")
	}
	if !strings.Contains(err.Error(), "write-once") {
		t.Errorf("error = %v, want it to name the write-once constraint", err)
	}
	_, taskID, _ := sessionLifecycleRow(t, db, "sess-A")
	if taskID == nil || *taskID != 100 {
		t.Errorf("task_id = %v, want 100 (the refused bind must not have moved it)", taskID)
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
// transition: tasks in untriaged/unplanned/ready/blocked/revisit flip to
// underway as part of the defense-in-depth mirror of claim_item events.
//
// `revisit` is in the set per E-1889: every reopen route now lands there, so
// without it `task spawn --reopen` would bind a session to a task that still
// reads as not-started.
func TestStartWorkSession_PromotesEligibleStatus(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	// Walked from the registry rather than hand-listed: ClaimPromotes IS the
	// set this helper's WHERE clause is built from, so a status added to (or
	// removed from) the group is covered here with no edit. E-2018 removed
	// `blocked`, which a hand-written list would have kept asserting.
	type promotes struct {
		taskID int64
		status string
	}
	var cases []promotes
	for i, status := range taskstatus.Get(taskstatus.ClaimPromotes) {
		cases = append(cases, promotes{taskID: int64(i + 1), status: status})
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
// clause: tasks in statuses outside taskstatus.ClaimPromotes (e.g.
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
// fresh session lands in working state with task_id=NULL.
func TestStartChatSession_InsertWithNullTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	t.Setenv("TMUX_PANE", "%5")

	if err := StartChatSession("sess-A", 1); err != nil {
		t.Fatalf("StartChatSession: %v", err)
	}
	state, taskID, process := sessionLifecycleRow(t, db, "sess-A")
	if state != "working" {
		t.Errorf("state = %q, want working", state)
	}
	if taskID != nil {
		t.Errorf("task_id = %v, want NULL", *taskID)
	}
	if process != "%5" {
		t.Errorf("process = %q, want %%5", process)
	}
}

// TestStartChatSession_UpsertKeepsTaskID pins E-1968 / ED-1560: starting a
// chat on a session already bound to a task must NOT drop the binding. The
// column is write-once, and `task chat` has nothing to say about who owns a
// task — clearing it here made the session that worked the task unreachable by
// task ref. The session still flips to 'working'; only the unbind is gone.
func TestStartChatSession_UpsertKeepsTaskID(t *testing.T) {
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
	state, taskID, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "working" {
		t.Errorf("state = %q, want working", state)
	}
	if taskID == nil || *taskID != 42 {
		t.Errorf("task_id = %v, want 42 (chat takeover must not unbind)", taskID)
	}
}

// TestInitSession_InsertCreatesIdle pins the SessionStart shape: first call
// creates the row in state='idle'.
//
// It wrote `needs_input` until E-2091. That was a claim about a person — the
// state means the AGENT asked the USER something — made by a writer that knows
// nothing of the kind, and the rows it produced were sessions that registered
// and never had a turn. `idle` is what a row that exists and has done nothing
// means, and this is the assertion that keeps a new writer from reintroducing
// the old one.
func TestInitSession_InsertCreatesIdle(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := InitSession("sess-A", 1); err != nil {
		t.Fatalf("InitSession: %v", err)
	}
	state, _, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "idle" {
		t.Errorf("state = %q, want idle", state)
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
	if s.TaskID == nil || *s.TaskID != 42 {
		t.Errorf("TaskID = %v, want 42", s.TaskID)
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

// TestCompleteTask_FlipsTaskAndIdlesSession pins the two-step write: the task
// moves to 'confirmed' and the session goes state='idle'. Per E-1968 /
// ED-1560 the binding SURVIVES — the session that confirmed the task is the
// session that worked it, and task_id is the only route back to its
// transcript (`session goto E-<id> --resume` resolves through it).
func TestCompleteTask_FlipsTaskAndIdlesSession(t *testing.T) {
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
	state, taskID, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "idle" {
		t.Errorf("session state = %q, want idle", state)
	}
	if taskID == nil || *taskID != 42 {
		t.Errorf("task_id = %v, want 42 (completion must not unbind)", taskID)
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
	state, taskID, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "idle" {
		t.Errorf("state = %q, want idle", state)
	}
	// task_id is intentionally preserved across idle — only
	// CompleteTask clears it. Pin that here so a future change has to
	// justify breaking the contract.
	if taskID == nil || *taskID != 42 {
		t.Errorf("task_id = %v, want 42 (idle should not clear)", taskID)
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

// ---------------------------------------------------------------------------
// E-2093: WakeSession — the missing half of Stop.
// ---------------------------------------------------------------------------

// TestWakeSession_WakesIdleSessionHoldingATask is the regression the two
// stranded sessions needed. `Stop` sets idle at the end of every turn; nothing
// set `working` back, so the write gate — which admitted `working` alone —
// refused every write a session made after its first clean turn.
func TestWakeSession_WakesIdleSessionHoldingATask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "underway")

	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("BindSessionToTask: %v", err)
	}
	if err := IdleSession("sess-A"); err != nil {
		t.Fatalf("IdleSession: %v", err)
	}
	if state, _, _ := sessionLifecycleRow(t, db, "sess-A"); state != "idle" {
		t.Fatalf("precondition: state = %q, want idle", state)
	}

	if err := WakeSession("sess-A"); err != nil {
		t.Fatalf("WakeSession: %v", err)
	}

	state, taskID, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "working" {
		t.Errorf("state = %q, want working — the session holds a task and was observed acting", state)
	}
	if taskID == nil || *taskID != 42 {
		t.Errorf("task_id = %v, want 42 — the wake must not touch the binding", taskID)
	}
}

// TestWakeSession_DoesNotWakeSessionHoldingNoTask pins the precondition. The
// wake RESTORES a declaration already made; it must never manufacture one. A
// session that never claimed is exactly what the gate exists to refuse, and
// waking it would admit it.
func TestWakeSession_DoesNotWakeSessionHoldingNoTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := StartChatSession("sess-A", 1); err != nil {
		t.Fatalf("StartChatSession: %v", err)
	}
	if err := IdleSession("sess-A"); err != nil {
		t.Fatalf("IdleSession: %v", err)
	}

	if err := WakeSession("sess-A"); err != nil {
		t.Fatalf("WakeSession: %v", err)
	}

	state, taskID, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != "idle" {
		t.Errorf("state = %q, want idle — a session holding no task is not woken", state)
	}
	if taskID != nil {
		t.Errorf("task_id = %v, want NULL", taskID)
	}
}

// ---------------------------------------------------------------------------
// PromptSession / ResumeFromPrompt (E-2091) — the state machine for a session
// blocked on a permission prompt.
// ---------------------------------------------------------------------------

// seedPromptSession binds a session to a task and forces it into `state`,
// returning the throwaway DB. The two helpers under test are unconditional and
// self-conditioning respectively, so every case starts from a known state
// rather than from whatever a lifecycle call left behind.
func seedPromptSession(t *testing.T, state string) *sql.DB {
	t.Helper()
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "underway")
	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("BindSessionToTask: %v", err)
	}
	if _, err := db.Exec(
		"UPDATE sessions SET state=? WHERE session_id=?", state, "sess-A",
	); err != nil {
		t.Fatalf("seed %s: %v", state, err)
	}
	return db
}

// TestPromptSession_WritesPromptedFromEveryState pins the unconditional write.
// A permission prompt is a fact about right now; what the row said a moment ago
// does not change it, which is the same rule IdleSession and EndSession follow.
func TestPromptSession_WritesPromptedFromEveryState(t *testing.T) {
	for _, state := range sessionstate.Get(sessionstate.All) {
		t.Run(state, func(t *testing.T) {
			db := seedPromptSession(t, state)

			if err := PromptSession("sess-A"); err != nil {
				t.Fatalf("PromptSession: %v", err)
			}
			if got, _, _ := sessionLifecycleRow(t, db, "sess-A"); got != sessionstate.Prompted {
				t.Errorf("state = %q, want %q", got, sessionstate.Prompted)
			}
		})
	}
}

// TestResumeFromPrompt_ClearsOnlyPrompted is the narrowness the design rests
// on. The WHERE clause is the whole rule, so the helper can only ever undo a
// state PromptSession wrote — it fires on every PostToolUse and every
// UserPromptSubmit, and an idle session between turns must survive both.
func TestResumeFromPrompt_ClearsOnlyPrompted(t *testing.T) {
	for _, state := range sessionstate.Get(sessionstate.All) {
		t.Run(state, func(t *testing.T) {
			db := seedPromptSession(t, state)

			if err := ResumeFromPrompt("sess-A"); err != nil {
				t.Fatalf("ResumeFromPrompt: %v", err)
			}
			want := state
			if state == sessionstate.Prompted {
				want = sessionstate.Working
			}
			if got, _, _ := sessionLifecycleRow(t, db, "sess-A"); got != want {
				t.Errorf("state = %q, want %q", got, want)
			}
		})
	}
}

// TestResumeFromPrompt_IsIdempotent: both clearing events can fire inside one
// turn, so a second call must be a no-op rather than a second transition.
func TestResumeFromPrompt_IsIdempotent(t *testing.T) {
	db := seedPromptSession(t, sessionstate.Prompted)

	for i := 0; i < 3; i++ {
		if err := ResumeFromPrompt("sess-A"); err != nil {
			t.Fatalf("ResumeFromPrompt %d: %v", i, err)
		}
	}
	if got, _, _ := sessionLifecycleRow(t, db, "sess-A"); got != sessionstate.Working {
		t.Errorf("state = %q, want %q", got, sessionstate.Working)
	}
}

// TestResumeFromPrompt_NeedsNoTask is the one place it is deliberately WIDER
// than WakeSession. That helper requires a task because it restores a
// DECLARATION and must never manufacture one. This restores nothing but the
// state it replaced, and a session prompted for a tool is exactly as entitled
// to `working` as it was a moment earlier.
func TestResumeFromPrompt_NeedsNoTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, started_at, last_activity)
		 VALUES ('sess-A', 1, 'claude', ?, '2026-09-01T00:00:00', '2026-09-01T00:00:00')`,
		sessionstate.Prompted,
	); err != nil {
		t.Fatalf("seed unbound session: %v", err)
	}

	if err := ResumeFromPrompt("sess-A"); err != nil {
		t.Fatalf("ResumeFromPrompt: %v", err)
	}
	state, taskID, _ := sessionLifecycleRow(t, db, "sess-A")
	if state != sessionstate.Working {
		t.Errorf("state = %q, want %q", state, sessionstate.Working)
	}
	if taskID != nil {
		t.Errorf("task_id = %v, want NULL — the resume must not manufacture a claim", taskID)
	}
}

// TestWakeSession_LeavesEveryOtherState pins what the wake must NOT touch.
//
// `needs_input` means a human was asked something and has not answered, and
// only the human answering ends it. `prompted` is the sharper case (E-2091):
// the hook calls WakeSession on EVERY event, so a wake that fired from
// `prompted` would clear the prompt on the very event that set it, and
// ResumeFromPrompt — which fires only where the answer actually arrived —
// would have nothing left to do. `ended` is left too: TouchSession revives it
// to `idle` earlier in the same event, and this helper then promotes it on its
// own terms.
//
// Derived from the vocabulary rather than listed, so a state added later is
// covered here without anyone remembering to add it.
func TestWakeSession_LeavesEveryOtherState(t *testing.T) {
	var others []string
	for _, state := range sessionstate.Get(sessionstate.All) {
		if state != sessionstate.Idle {
			others = append(others, state)
		}
	}
	for _, state := range others {
		t.Run(state, func(t *testing.T) {
			db := withTestDB(t)
			seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
			seedTask(t, db, 42, 1, "test task", "underway")
			if err := BindSessionToTask("sess-A", 1, 42); err != nil {
				t.Fatalf("BindSessionToTask: %v", err)
			}
			if _, err := db.Exec(
				"UPDATE sessions SET state=? WHERE session_id=?", state, "sess-A",
			); err != nil {
				t.Fatalf("seed %s: %v", state, err)
			}

			if err := WakeSession("sess-A"); err != nil {
				t.Fatalf("WakeSession: %v", err)
			}

			if got, _, _ := sessionLifecycleRow(t, db, "sess-A"); got != state {
				t.Errorf("state = %q, want %q untouched", got, state)
			}
		})
	}
}

// TestWakeSession_IsIdempotent pins that a second call is a no-op rather than
// a second transition. The hook fires it on every event of every turn, so this
// is the common case, not an edge one.
func TestWakeSession_IsIdempotent(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "underway")
	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("BindSessionToTask: %v", err)
	}
	if err := IdleSession("sess-A"); err != nil {
		t.Fatalf("IdleSession: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := WakeSession("sess-A"); err != nil {
			t.Fatalf("WakeSession #%d: %v", i, err)
		}
	}
	if state, _, _ := sessionLifecycleRow(t, db, "sess-A"); state != "working" {
		t.Errorf("state = %q, want working", state)
	}
}

// TestWakeSession_UnknownSessionIsNotAnError pins the fail-soft shape. The
// hook calls this on every event, including ones for a session whose row does
// not exist yet; an error there would take the session down over a state
// refresh the next event would have fixed anyway.
func TestWakeSession_UnknownSessionIsNotAnError(t *testing.T) {
	withTestDB(t)
	if err := WakeSession("sess-never-seen"); err != nil {
		t.Errorf("WakeSession on an unknown session = %v, want nil", err)
	}
}

// TestTouchSession_DoesNotWakeIdle is the guard for where the wake must NOT
// live, and it is not hypothetical: the wake was written inside TouchSession
// first, and this is what it broke.
//
// TouchSession is reached by callers that are NOT the session. A sibling shell
// pane resolves a Claude session's row through EnsureClaudeSessionID, by
// reading the window's published UUID — that caller has observed nothing about
// whether the session is acting. With the wake in TouchSession, every `endless`
// command a human ran in the shell pane woke the sibling session, and the
// monitor pane beside it did the same on every repaint, so an idle session read
// as `working` forever and `project status` lost the distinction entirely.
func TestTouchSession_DoesNotWakeIdle(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "underway")
	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("BindSessionToTask: %v", err)
	}
	if err := IdleSession("sess-A"); err != nil {
		t.Fatalf("IdleSession: %v", err)
	}

	// Both routes into TouchSession, including the sibling-shell resolver.
	if err := TouchSession("sess-A", "claude", "", 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	if _, err := EnsureClaudeSessionID("sess-A", "", 1); err != nil {
		t.Fatalf("EnsureClaudeSessionID: %v", err)
	}

	if state, _, _ := sessionLifecycleRow(t, db, "sess-A"); state != "idle" {
		t.Errorf("state = %q, want idle — TouchSession must not wake; only the hook may", state)
	}
}

// TestTouchSession_EndedRevivalStillLandsNeutral guards E-1686 alongside the
// wake: an `ended` row is revived to the neutral state, never straight to
// `working`. That state is `idle` since E-2091 — TouchSession itself still
// makes no promotion, which is the property under test; the hook's own
// WakeSession call is what may promote afterwards, on its own preconditions.
func TestTouchSession_EndedRevivalStillLandsNeutral(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "underway")

	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("BindSessionToTask: %v", err)
	}
	if err := EndSession("sess-A"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	if err := TouchSession("sess-A", "claude", "", 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	if state, _, _ := sessionLifecycleRow(t, db, "sess-A"); state != "idle" {
		t.Errorf("state = %q, want idle", state)
	}
}

// TestWakeSession_StopStillEndsIdle pins the Stop ordering. The hook wakes on
// every event — Stop included — and then idles, so the turn still ends idle.
// If that order ever inverted, every session would read as permanently working
// and `session status` would say so.
func TestWakeSession_StopStillEndsIdle(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 42, 1, "test task", "underway")

	if err := BindSessionToTask("sess-A", 1, 42); err != nil {
		t.Fatalf("BindSessionToTask: %v", err)
	}
	// The Stop event, in the order `hook claude` performs it.
	if err := TouchSession("sess-A", "claude", "", 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	if err := WakeSession("sess-A"); err != nil {
		t.Fatalf("WakeSession: %v", err)
	}
	if err := IdleSession("sess-A"); err != nil {
		t.Fatalf("IdleSession: %v", err)
	}

	if state, _, _ := sessionLifecycleRow(t, db, "sess-A"); state != "idle" {
		t.Errorf("state = %q, want idle — Stop must still end the turn idle", state)
	}
}
