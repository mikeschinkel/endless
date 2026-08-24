package monitor

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

// readLogEntries reads every line of the machine-local diagnostic log at the
// current ConfigDir() and decodes each into a generic map. Absent file → nil.
func readLogEntries(t *testing.T) []map[string]any {
	t.Helper()
	f, err := os.Open(userMachineLogPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("decode log line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

// seedProjectTask inserts project 1 and a task so a session row's project_id /
// task_id foreign keys resolve.
func seedProjectTask(t *testing.T, taskID int64) {
	t.Helper()
	db, err := DB()
	if err != nil {
		t.Fatalf("DB: %v", err)
	}
	db.Exec("INSERT OR IGNORE INTO projects (id, name, path) VALUES (1, 'p', '/p')")
	if _, err := db.Exec(
		"INSERT OR IGNORE INTO tasks (id, project_id, title, status) VALUES (?, 1, 't', 'underway')",
		taskID,
	); err != nil {
		t.Fatalf("seed task %d: %v", taskID, err)
	}
}

// seedSession inserts one session row for snapshot/transition tests.
func seedSession(t *testing.T, guid, short, state string, taskID any) {
	t.Helper()
	db, err := DB()
	if err != nil {
		t.Fatalf("DB: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, short_id, project_id, state, task_id, kind_id)
		 VALUES (?, ?, 1, ?, ?, 1)`,
		guid, short, state, taskID,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

// TestLogSessionTxn_WritesJSONLine confirms one transition produces one
// well-formed line carrying the kind discriminator and the old→new fields.
func TestLogSessionTxn_WritesJSONLine(t *testing.T) {
	withTestDB(t)

	old := int64(1835)
	nw := int64(1832)
	LogSessionTxn(SessionTxn{
		SessionGUID: "guid-A",
		ShortID:     "abc123",
		OldState:    "working",
		NewState:    "working",
		OldTaskID:   &old,
		NewTaskID:   &nw,
		Reason:      SessionLogCwdBind,
		Caller:      "test",
	})

	entries := readLogEntries(t)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e["kind"] != "session" {
		t.Errorf("kind = %v, want session", e["kind"])
	}
	if e["reason"] != "cwd-bind" {
		t.Errorf("reason = %v, want cwd-bind", e["reason"])
	}
	if e["old_task_id"] != float64(1835) {
		t.Errorf("old_task_id = %v, want 1835", e["old_task_id"])
	}
	if e["new_task_id"] != float64(1832) {
		t.Errorf("new_task_id = %v, want 1832", e["new_task_id"])
	}
	if e["ts"] == "" || e["ts"] == nil {
		t.Errorf("ts missing")
	}
}

// TestSnapshotSession_CapturesOldState confirms the pre-write read returns the
// session's current state and task_id.
func TestSnapshotSession_CapturesOldState(t *testing.T) {
	withTestDB(t)
	seedProjectTask(t, 77)
	seedSession(t, "guid-B", "short-B", "idle", int64(77))

	snap := SnapshotSession("guid-B")
	if !snap.Found {
		t.Fatalf("Found = false, want true")
	}
	if snap.State != "idle" {
		t.Errorf("State = %q, want idle", snap.State)
	}
	if snap.ShortID != "short-B" {
		t.Errorf("ShortID = %q, want short-B", snap.ShortID)
	}
	if snap.TaskID == nil || *snap.TaskID != 77 {
		t.Errorf("TaskID = %v, want 77", snap.TaskID)
	}
}

// TestSnapshotSession_MissingRow yields a not-found snapshot with zero fields,
// so a first-time bind logs empty old state rather than crashing.
func TestSnapshotSession_MissingRow(t *testing.T) {
	withTestDB(t)
	snap := SnapshotSession("nope")
	if snap.Found {
		t.Errorf("Found = true, want false")
	}
	if snap.TaskID != nil {
		t.Errorf("TaskID = %v, want nil", snap.TaskID)
	}
}

// TestIdleSession_LogsTransition confirms IdleSession records old→new state.
func TestIdleSession_LogsTransition(t *testing.T) {
	withTestDB(t)
	seedProjectTask(t, 5)
	seedSession(t, "guid-C", "short-C", "working", int64(5))

	if err := IdleSession("guid-C"); err != nil {
		t.Fatalf("IdleSession: %v", err)
	}
	entries := readLogEntries(t)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e["reason"] != "idle" {
		t.Errorf("reason = %v, want idle", e["reason"])
	}
	if e["old_state"] != "working" || e["new_state"] != "idle" {
		t.Errorf("state transition = %v→%v, want working→idle", e["old_state"], e["new_state"])
	}
}

// TestEndSession_LogsTransition confirms EndSession records the end transition.
func TestEndSession_LogsTransition(t *testing.T) {
	withTestDB(t)
	seedProjectTask(t, 9)
	seedSession(t, "guid-D", "short-D", "idle", int64(9))

	if err := EndSession("guid-D"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	entries := readLogEntries(t)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0]["reason"] != "end" || entries[0]["new_state"] != "ended" {
		t.Errorf("entry = %v, want reason=end new_state=ended", entries[0])
	}
}

// TestBindSessionToTask_LogsDedup confirms the paneless-dedup UPDATE emits one
// diagnostic line per silently-ended stale row — the trail the incident lacked.
func TestBindSessionToTask_LogsDedup(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/p")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (42, 1, 't', 'underway')",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	// A stale paneless tmux row already bound to task 42.
	seedSession(t, "uuid-old", "short-old", "working", int64(42))

	// A fresh session binds to the same task; the dedup ends uuid-old.
	if err := BindSessionToTask("uuid-new", 1, 42); err != nil {
		t.Fatalf("BindSessionToTask: %v", err)
	}

	var dedup map[string]any
	for _, e := range readLogEntries(t) {
		if e["reason"] == "dedup" {
			dedup = e
		}
	}
	if dedup == nil {
		t.Fatalf("no dedup log line emitted; entries=%v", readLogEntries(t))
	}
	if dedup["session_id"] != "uuid-old" {
		t.Errorf("dedup session_id = %v, want uuid-old", dedup["session_id"])
	}
	if dedup["new_state"] != "ended" {
		t.Errorf("dedup new_state = %v, want ended", dedup["new_state"])
	}
}
