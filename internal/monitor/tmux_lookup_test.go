package monitor

import (
	"database/sql"
	"errors"
	"testing"
)

// fakePane returns a tmux pane id that definitely does not exist on the
// host's tmux server, so the window-scoped fallback's `tmux list-panes`
// invocation fails fast (no spurious matches) and the caller gets the
// pure-DB result. The high pane index makes accidental collision with a
// real pane essentially impossible during test runs.
const fakePane = "%999991"
const fakePane2 = "%999992"

// TestGetActiveTaskForPane_DirectMatch pins the primary lookup: a sessions
// row whose process equals the pane id and whose active_task_id is set
// returns the joined task info.
func TestGetActiveTaskForPane_DirectMatch(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status, type, phase) VALUES (?, ?, ?, ?, ?, ?)",
		55, 1, "build the widget", "in_progress", "task", "now",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, active_task_id, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, ?, '2026-05-20T00:00:00')`,
		"sess-A", 1, fakePane, 55,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	info, err := GetActiveTaskForPane(fakePane)
	if err != nil {
		t.Fatalf("GetActiveTaskForPane: %v", err)
	}
	if info.TaskID != 55 {
		t.Errorf("TaskID = %d, want 55", info.TaskID)
	}
	if info.Title != "build the widget" {
		t.Errorf("Title = %q, want %q", info.Title, "build the widget")
	}
	if info.Status != "in_progress" {
		t.Errorf("Status = %q, want in_progress", info.Status)
	}
	if info.ProjectName != "acme" {
		t.Errorf("ProjectName = %q, want acme", info.ProjectName)
	}
}

// TestGetActiveTaskForPane_EmptyPaneReturnsErrNoActiveTask pins the empty
// input branch: callers without a pane id (non-tmux contexts) get
// ErrNoActiveTask before any DB or tmux work happens.
func TestGetActiveTaskForPane_EmptyPaneReturnsErrNoActiveTask(t *testing.T) {
	withTestDB(t)
	_, err := GetActiveTaskForPane("")
	if !errors.Is(err, ErrNoActiveTask) {
		t.Errorf("empty pane: got %v, want ErrNoActiveTask", err)
	}
}

// TestGetActiveTaskForPane_NoMatchReturnsErrNoActiveTask pins the no-row
// case: a pane id with no matching session (and a tmux list-panes that
// fails for the synthetic id) surfaces ErrNoActiveTask, not an internal
// error.
func TestGetActiveTaskForPane_NoMatchReturnsErrNoActiveTask(t *testing.T) {
	withTestDB(t)
	_, err := GetActiveTaskForPane(fakePane)
	if !errors.Is(err, ErrNoActiveTask) {
		t.Errorf("unknown pane: got %v, want ErrNoActiveTask", err)
	}
}

// TestGetActiveTaskForPane_SkipsNullActiveTask pins that a session row
// with NULL active_task_id is not selected — only sessions with a bound
// task are eligible, even if process matches exactly.
func TestGetActiveTaskForPane_SkipsNullActiveTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, '2026-05-20T00:00:00')`,
		"sess-B", 1, fakePane,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	_, err := GetActiveTaskForPane(fakePane)
	if !errors.Is(err, ErrNoActiveTask) {
		t.Errorf("NULL active_task_id: got %v, want ErrNoActiveTask", err)
	}
}

// TestGetPaneStatus_EmptyPaneReturnsNone pins the early-out branch:
// callers without a pane id get PaneStatusNone with nil error so the
// status renderer always has something to display.
func TestGetPaneStatus_EmptyPaneReturnsNone(t *testing.T) {
	withTestDB(t)
	ps, err := GetPaneStatus("")
	if err != nil {
		t.Fatalf("GetPaneStatus(\"\"): %v", err)
	}
	if ps.Kind != PaneStatusNone {
		t.Errorf("Kind = %d, want PaneStatusNone (%d)", ps.Kind, PaneStatusNone)
	}
}

// TestGetPaneStatus_ActiveTaskReturnsActiveKind pins the happy path: a
// bound session in the pane produces PaneStatusActive with the task info
// populated.
func TestGetPaneStatus_ActiveTaskReturnsActiveKind(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status, type, phase) VALUES (?, ?, ?, ?, ?, ?)",
		66, 1, "ship it", "in_progress", "task", "now",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, active_task_id, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, ?, '2026-05-20T00:00:00')`,
		"sess-A", 1, fakePane, 66,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	ps, err := GetPaneStatus(fakePane)
	if err != nil {
		t.Fatalf("GetPaneStatus: %v", err)
	}
	if ps.Kind != PaneStatusActive {
		t.Errorf("Kind = %d, want PaneStatusActive (%d)", ps.Kind, PaneStatusActive)
	}
	if ps.Task == nil {
		t.Fatalf("Task = nil, want populated")
	}
	if ps.Task.TaskID != 66 {
		t.Errorf("Task.TaskID = %d, want 66", ps.Task.TaskID)
	}
}

// TestGetPaneStatus_SessionWithoutTaskReturnsNoTaskKind pins the
// "session exists but no active task" branch: a sessions row with NULL
// active_task_id for the given pane drives the "claim a task" hint.
func TestGetPaneStatus_SessionWithoutTaskReturnsNoTaskKind(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, '2026-05-20T00:00:00')`,
		"sess-noTask", 1, fakePane,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	ps, err := GetPaneStatus(fakePane)
	if err != nil {
		t.Fatalf("GetPaneStatus: %v", err)
	}
	if ps.Kind != PaneStatusNoTask {
		t.Errorf("Kind = %d, want PaneStatusNoTask (%d)", ps.Kind, PaneStatusNoTask)
	}
}

// TestGetLiveSessionByProcess_ReturnsLiveID pins the happy path: a live
// (state!='ended') session whose process matches returns its id.
func TestGetLiveSessionByProcess_ReturnsLiveID(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	res, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, '2026-05-20T00:00:00')`,
		"sess-live", 1, fakePane,
	)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	wantID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}

	gotID, err := GetLiveSessionByProcess(fakePane)
	if err != nil {
		t.Fatalf("GetLiveSessionByProcess: %v", err)
	}
	if gotID != wantID {
		t.Errorf("got id %d, want %d", gotID, wantID)
	}
}

// TestGetLiveSessionByProcess_FiltersEnded pins the state filter: a
// session matching by process but with state='ended' must not be
// returned — the live binding is the contract.
func TestGetLiveSessionByProcess_FiltersEnded(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
		 VALUES (?, ?, 'claude', 'ended', ?, '2026-05-20T00:00:00')`,
		"sess-dead", 1, fakePane,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	_, err := GetLiveSessionByProcess(fakePane)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("ended-only match: got %v, want sql.ErrNoRows", err)
	}
}

// TestGetLiveSessionByProcess_PicksMostRecent pins the
// ORDER BY last_activity DESC LIMIT 1 contract: multiple live sessions
// on the same process (a transient state during pane reattach) resolve
// to the most recently active row.
func TestGetLiveSessionByProcess_PicksMostRecent(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, '2026-05-19T00:00:00')`,
		"sess-old", 1, fakePane,
	); err != nil {
		t.Fatalf("seed old: %v", err)
	}
	res, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, '2026-05-21T00:00:00')`,
		"sess-new", 1, fakePane,
	)
	if err != nil {
		t.Fatalf("seed new: %v", err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}

	gotID, err := GetLiveSessionByProcess(fakePane)
	if err != nil {
		t.Fatalf("GetLiveSessionByProcess: %v", err)
	}
	if gotID != newID {
		t.Errorf("got id %d, want most-recent id %d", gotID, newID)
	}
}

// TestGetLiveSessionByProcess_EmptyInputReturnsErrNoRows pins the input
// guard: an empty process string skips the query and returns
// sql.ErrNoRows so callers can branch uniformly.
func TestGetLiveSessionByProcess_EmptyInputReturnsErrNoRows(t *testing.T) {
	withTestDB(t)
	_, err := GetLiveSessionByProcess("")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("empty input: got %v, want sql.ErrNoRows", err)
	}
}

// _ = fakePane2 keeps the second sentinel exported for future cases where
// two synthetic panes are needed in the same test (e.g., differentiating
// pane vs window fallback). Removed once a test consumes it; harmless here.
var _ = fakePane2
