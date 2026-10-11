package events

import (
	"database/sql"
	"strings"
	"testing"
)

func countPair(t *testing.T, db *sql.DB, table string, sessionID, taskID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM `+table+` WHERE session_id = ? AND task_id = ?`,
		sessionID, taskID,
	).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func emitVisibility(t *testing.T, db *sql.DB, kind Kind, sessionID int64, ids ...string) (*ExecuteResult, error) {
	t.Helper()
	return dispatch(db, membershipEvent(t, kind, sessionID, ids), nil)
}

// TestSessionTasksHidden_HidesAndClearsPin: hide writes the hide and drops any
// pin, so a pair is never both.
func TestSessionTasksHidden_HidesAndClearsPin(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	if _, err := db.Exec(
		`INSERT INTO session_pinned_tasks (session_id, task_id, pinned_at) VALUES (42, 100, 'x')`,
	); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	res, err := emitVisibility(t, db, KindSessionTasksHidden, 42, "E-100")
	if err != nil {
		t.Fatalf("hide: %v", err)
	}
	if countPair(t, db, "session_hidden_tasks", 42, 100) != 1 || countPair(t, db, "session_pinned_tasks", 42, 100) != 0 {
		t.Errorf("after hide: want hidden and unpinned")
	}
	if !strings.Contains(res.Markdown, "Hid E-100 for ES-42") {
		t.Errorf("markdown = %q", res.Markdown)
	}

	res, err = emitVisibility(t, db, KindSessionTasksHidden, 42, "E-100")
	if err != nil {
		t.Fatalf("re-hide: %v", err)
	}
	if !strings.Contains(res.Markdown, "already hidden") {
		t.Errorf("re-hide markdown = %q, want a no-op report", res.Markdown)
	}
}

// TestSessionTasksHidden_FocusIsHideable: a task that is only the session's
// focus pointer hides like any other (E-2293 §3).
func TestSessionTasksHidden_FocusIsHideable(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	seedLiveTask(t, db, 101)
	if _, err := db.Exec(`UPDATE sessions SET task_id = 100, focus_task_id = 101 WHERE id = 42`); err != nil {
		t.Fatalf("set task/focus: %v", err)
	}
	if _, err := emitVisibility(t, db, KindSessionTasksHidden, 42, "E-101"); err != nil {
		t.Fatalf("hide focus: %v", err)
	}
	if countPair(t, db, "session_hidden_tasks", 42, 101) != 1 {
		t.Errorf("focus-only task was not hidden")
	}
}

// TestSessionTasksHidden_RefusesOwnTask: the task the session claimed is its
// board's anchor and cannot be hidden; nothing in the request is applied.
func TestSessionTasksHidden_RefusesOwnTask(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	seedLiveTask(t, db, 101)
	if _, err := db.Exec(`UPDATE sessions SET task_id = 100 WHERE id = 42`); err != nil {
		t.Fatalf("set task: %v", err)
	}
	_, err := emitVisibility(t, db, KindSessionTasksHidden, 42, "E-101", "E-100")
	if err == nil || !strings.Contains(err.Error(), "E-100") {
		t.Fatalf("hide own task: err = %v, want a refusal naming E-100", err)
	}
	if countPair(t, db, "session_hidden_tasks", 42, 101) != 0 {
		t.Errorf("a refused request still hid E-101")
	}
}

// TestSessionTasksUnhidden_UnhidesOrPins: unhide clears a hide; on a task that
// is not hidden it pins it instead (E-2293 §5), and pinning twice is a no-op.
func TestSessionTasksUnhidden_UnhidesOrPins(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	seedLiveTask(t, db, 101)
	if _, err := emitVisibility(t, db, KindSessionTasksHidden, 42, "E-100"); err != nil {
		t.Fatalf("hide: %v", err)
	}

	res, err := emitVisibility(t, db, KindSessionTasksUnhidden, 42, "E-100", "E-101")
	if err != nil {
		t.Fatalf("unhide: %v", err)
	}
	if countPair(t, db, "session_hidden_tasks", 42, 100) != 0 || countPair(t, db, "session_pinned_tasks", 42, 100) != 0 {
		t.Errorf("E-100: want unhidden and NOT pinned")
	}
	if countPair(t, db, "session_pinned_tasks", 42, 101) != 1 {
		t.Errorf("E-101: want pinned")
	}
	if !strings.Contains(res.Markdown, "Unhid E-100") || !strings.Contains(res.Markdown, "Pinned E-101") {
		t.Errorf("markdown = %q", res.Markdown)
	}

	res, err = emitVisibility(t, db, KindSessionTasksUnhidden, 42, "E-101")
	if err != nil {
		t.Fatalf("re-pin: %v", err)
	}
	if !strings.Contains(res.Markdown, "already pinned") {
		t.Errorf("re-pin markdown = %q", res.Markdown)
	}
}

// TestSessionTasksUnhidden_RefusesUnknownTask: a typo fails the whole call.
func TestSessionTasksUnhidden_RefusesUnknownTask(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	if _, err := emitVisibility(t, db, KindSessionTasksUnhidden, 42, "E-999"); err == nil {
		t.Fatal("unhide of an unknown task: want a refusal")
	}
}
