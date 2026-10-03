package monitor

import (
	"database/sql"
	"strconv"
	"testing"
)

// The reads behind `endless project status` / `endless project monitor`
// (E-1976).

func TestSanitizeTmuxName(t *testing.T) {
	tests := []struct{ project, want string }{
		{"endless", "endless"},
		{"go-tealeaves", "go-tealeaves"},
		// tmux forbids '.' and ':' in a session name — both are target
		// separators, so a name containing one addresses something else.
		{"my.project", "my-project"},
		{"ns:proj", "ns-proj"},
		// PRODUCT: project names are user-chosen and arrive with spaces and
		// slashes in them. None of that may be a rule the user has to know.
		{"My Cool App", "My-Cool-App"},
		{"a/b/c", "a-b-c"},
		// A leading '-' would be read as a flag by tmux, so the fold is trimmed.
		{"-weird-", "weird"},
		// Every character folded away. The monitor still has to be reachable.
		{"...", "project"},
		{"", "project"},
	}
	for _, tt := range tests {
		if got := SanitizeTmuxName(tt.project); got != tt.want {
			t.Errorf("SanitizeTmuxName(%q) = %q, want %q", tt.project, got, tt.want)
		}
	}
}

// seedProjectStatusTask inserts one `now` task for these queries to find.
func seedProjectStatusTask(t *testing.T, db *sql.DB, id, projectID int64, status string) {
	t.Helper()
	seedProjectStatusTaskIn(t, db, id, projectID, status, "now")
}

func seedProjectStatusTaskIn(t *testing.T, db *sql.DB, id, projectID int64, status, phase string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, status, phase, type_id, updated_at)
		 VALUES (?, ?, ?, ?, ?, 1, '2026-08-30T10:00:00')`,
		id, projectID, "task "+status, status, phase,
	); err != nil {
		t.Fatalf("seed task %d: %v", id, err)
	}
}

// seedProjectStatusSession inserts one session, optionally bound to a task.
func seedProjectStatusSession(t *testing.T, db *sql.DB, id, projectID int64, state string, taskID *int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, task_id, started_at, last_activity)
		 VALUES (?, ?, ?, ?, ?, '2026-08-30T09:00:00', '2026-08-30T11:00:00')`,
		id, "uuid-"+state+"-"+strconv.FormatInt(id, 10), projectID, state, taskID,
	); err != nil {
		t.Fatalf("seed session %d: %v", id, err)
	}
}

var nowNext = []string{"urgent", "now", "next"}

func projectStatusRows(t *testing.T, projectID int64, phases []string) []ProjectStatusRow {
	t.Helper()
	rows, err := ProjectStatusRows(projectID, phases)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	return rows
}

// TestProjectStatusRowsAreTasksOnly: a session is consulted, never a row (E-2156).
// A session holding no task contributes nothing; a session holding a task
// contributes a flag on that task's row, not a second row.
func TestProjectStatusRowsAreTasksOnly(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedProjectStatusTask(t, db, 1, 1, "underway")
	taskID := int64(1)
	seedProjectStatusSession(t, db, 10, 1, "working", &taskID)
	seedProjectStatusSession(t, db, 11, 1, "idle", nil)

	rows := projectStatusRows(t, 1, nowNext)
	if len(rows) != 1 || rows[0].TaskID != 1 {
		t.Fatalf("rows = %+v, want exactly task 1", rows)
	}
	if !rows[0].LiveSession {
		t.Errorf("task 1 is held by a working session but LiveSession is false")
	}
	if rows[0].TypeSlug != "todo" {
		t.Errorf("row lost its task type: %q", rows[0].TypeSlug)
	}
}

// TestProjectStatusRowsSelectsNonTerminalInPhases pins the row set: every
// non-terminal status, in the phases asked for, and nothing else.
func TestProjectStatusRowsSelectsNonTerminalInPhases(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	open := []string{"unplanned", "submitted", "ready", "underway", "unverified", "unreviewed", "revisit"}
	for i, status := range open {
		seedProjectStatusTask(t, db, int64(i+1), 1, status)
	}
	seedProjectStatusTask(t, db, 50, 1, "confirmed")
	seedProjectStatusTask(t, db, 51, 1, "declined")
	seedProjectStatusTaskIn(t, db, 60, 1, "ready", "urgent")
	seedProjectStatusTaskIn(t, db, 61, 1, "ready", "next")
	seedProjectStatusTaskIn(t, db, 62, 1, "ready", "later")
	seedProjectStatusTaskIn(t, db, 63, 1, "ready", "maybe")

	got := map[int64]bool{}
	for _, r := range projectStatusRows(t, 1, nowNext) {
		got[r.TaskID] = true
	}
	for i := range open {
		if !got[int64(i+1)] {
			t.Errorf("non-terminal %q task is missing", open[i])
		}
	}
	for _, id := range []int64{60, 61} {
		if !got[id] {
			t.Errorf("task %d (urgent/next) is missing", id)
		}
	}
	for _, id := range []int64{50, 51, 62, 63} {
		if got[id] {
			t.Errorf("task %d must not be in the urgent/now/next set", id)
		}
	}

	later := projectStatusRows(t, 1, []string{"later"})
	if len(later) != 1 || later[0].TaskID != 62 {
		t.Errorf("later set = %+v, want only task 62", later)
	}
}

// TestProjectStatusRowsLiveSessionFlags: only a LIVE session marks its task,
// and a prompted one marks it Prompted too. An ended session is history; a
// hidden one is still holding the work.
func TestProjectStatusRowsLiveSessionFlags(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	for id := int64(1); id <= 4; id++ {
		seedProjectStatusTask(t, db, id, 1, "underway")
	}
	one, two, three := int64(1), int64(2), int64(3)
	seedProjectStatusSession(t, db, 10, 1, "idle", &one)
	seedProjectStatusSession(t, db, 11, 1, "prompted", &two)
	seedProjectStatusSession(t, db, 12, 1, "ended", &three)
	seedProjectStatusSession(t, db, 13, 1, "idle", &three)
	if _, err := db.Exec("UPDATE sessions SET hidden = 1 WHERE id = 13"); err != nil {
		t.Fatalf("hide session: %v", err)
	}

	by := map[int64]ProjectStatusRow{}
	for _, r := range projectStatusRows(t, 1, nowNext) {
		by[r.TaskID] = r
	}
	want := map[int64][2]bool{1: {true, false}, 2: {true, true}, 3: {true, false}, 4: {false, false}}
	for id, w := range want {
		r := by[id]
		if r.LiveSession != w[0] || r.Prompted != w[1] {
			t.Errorf("task %d: live=%v prompted=%v, want live=%v prompted=%v",
				id, r.LiveSession, r.Prompted, w[0], w[1])
		}
	}
}

// TestProjectStatusRowsIgnoresDeadSessions pins the liveness filter, on the
// same terms ListLiveSessions applies it: `dead` means we REACHED the session's
// tmux server and its pane was not there, so the task it held has stalled.
// `unknown` — server unreachable — still counts as live (E-1898).
func TestProjectStatusRowsIgnoresDeadSessions(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedProjectStatusTask(t, db, 1, 1, "underway")
	seedProjectStatusTask(t, db, 2, 1, "underway")
	one, two := int64(1), int64(2)
	seedProjectStatusSession(t, db, 10, 1, "idle", &one)
	seedProjectStatusSession(t, db, 11, 1, "idle", &two)
	bindSessionPane(t, db, 10, "%1")
	bindSessionPane(t, db, 11, "%2")

	// A reachable server carrying only %1: session 10 is live, 11 is dead.
	restore := SetTestTmuxObservation(TestServerUUID, map[string]string{"%1": "claude"})
	defer restore()

	by := map[int64]bool{}
	for _, r := range projectStatusRows(t, 1, nowNext) {
		by[r.TaskID] = r.LiveSession
	}
	if !by[1] || by[2] {
		t.Fatalf("live flags = %v, want task 1 live and task 2 stalled", by)
	}
}

// bindSessionPane gives a seeded session a pane binding on the test server, so the
// liveness view has something to judge.
func bindSessionPane(t *testing.T, db *sql.DB, sessionID int64, address string) {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO processes (kind_id, server_uuid, address) VALUES (1, ?, ?)`,
		TestServerUUID, address,
	)
	if err != nil {
		t.Fatalf("seed process %s: %v", address, err)
	}
	pid, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("process id: %v", err)
	}
	if _, err = db.Exec(
		"UPDATE sessions SET process_id = ? WHERE id = ?", pid, sessionID,
	); err != nil {
		t.Fatalf("bind session %d: %v", sessionID, err)
	}
}

// TestProjectStatusRowsScopesToOneProject: the query is project-scoped, and a
// row leaking in from another project would be a claim on attention the user
// cannot act on from here.
func TestProjectStatusRowsScopesToOneProject(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedProject(t, db, 2, "q", "/tmp/q")
	seedProjectStatusTask(t, db, 1, 1, "unverified")
	seedProjectStatusTask(t, db, 2, 2, "unverified")
	seedProjectStatusSession(t, db, 10, 2, "idle", nil)

	rows, err := ProjectStatusRows(1, nowNext)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	if len(rows) != 1 || rows[0].TaskID != 1 {
		t.Fatalf("rows = %+v, want only project 1's task", rows)
	}
}

// TestProjectStatusRowsOmitsRemovedTasks: reads go through live_tasks, so a
// removed task cannot leak back into the row set (E-1929).
func TestProjectStatusRowsOmitsRemovedTasks(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedProjectStatusTask(t, db, 1, 1, "unverified")
	seedProjectStatusTask(t, db, 2, 1, "unverified")
	if _, err := db.Exec("UPDATE tasks SET removed = 1 WHERE id = 2"); err != nil {
		t.Fatalf("remove task: %v", err)
	}
	rows, err := ProjectStatusRows(1, nowNext)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	if len(rows) != 1 || rows[0].TaskID != 1 {
		t.Fatalf("rows = %+v, want only the live task", rows)
	}
}

// TestProjectByNameIsReadOnly is the guard that separates this resolver from
// ProjectIDForPath: a read view that auto-registered would mint a project row
// every time someone ran it in the wrong directory.
func TestProjectByNameIsReadOnly(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")

	if _, _, err := ProjectByName("no-such-project"); err == nil {
		t.Fatal("ProjectByName invented a project instead of refusing")
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM projects").Scan(&n); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if n != 1 {
		t.Errorf("a failed lookup wrote %d project rows", n-1)
	}

	id, name, err := ProjectByName("p")
	if err != nil || id != 1 || name != "p" {
		t.Errorf("ProjectByName(p) = (%d, %q, %v), want (1, p, nil)", id, name, err)
	}
}
