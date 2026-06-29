package monitor

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/navvia"
)

// seedNavTask inserts a minimal task so a session's active_task_id FK resolves.
func seedNavTask(t *testing.T, db *sql.DB, id, projectID int64, title string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title) VALUES (?, ?, ?)",
		id, projectID, title,
	); err != nil {
		t.Fatalf("seed task id=%d: %v", id, err)
	}
}

// seedNavSession inserts a live session bound to a pane, with an optional active
// task and summary, returning the new sessions.id.
func seedNavSession(t *testing.T, db *sql.DB, sessionID string, projectID int64, pane string, taskID int64, summary string) int64 {
	t.Helper()
	var task any
	if taskID != 0 {
		task = taskID
	}
	res, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, active_task_id, process, last_activity, summary)
		 VALUES (?, ?, 'claude', 'working', ?, ?, '2026-06-29T00:00:00', ?)`,
		sessionID, projectID, task, pane, summary,
	)
	if err != nil {
		t.Fatalf("seed session %q: %v", sessionID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// TestRecordNav_ResolvesEndpointsChainsAndTags is the core write-path shape:
// the first move has a NULL source, each subsequent move's source is the
// client's previous destination, the destination pane resolves to its tracked
// session + project, and the via marker is recorded.
func TestRecordNav_ResolvesEndpointsChainsAndTags(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj", "/tmp/proj")
	seedNavTask(t, db, 100, 1, "Task 100")
	seedNavTask(t, db, 200, 1, "Task 200")
	s1 := seedNavSession(t, db, "uuid-1", 1, "%1", 100, "working on 100")
	s2 := seedNavSession(t, db, "uuid-2", 1, "%2", 200, "working on 200")

	// First move: source is NULL (no prior row for this client).
	id1, err := RecordNav("client-a", "%1", navvia.NavViaManual)
	if err != nil {
		t.Fatalf("RecordNav first: %v", err)
	}
	if id1 == 0 {
		t.Fatal("first RecordNav returned 0 (expected an inserted row)")
	}

	// Second move to a different tracked pane, tagged goto.
	id2, err := RecordNav("client-a", "%2", navvia.NavViaGoto)
	if err != nil {
		t.Fatalf("RecordNav second: %v", err)
	}
	if id2 == 0 {
		t.Fatal("second RecordNav returned 0 (expected an inserted row)")
	}

	type row struct {
		fromSession *int64
		fromPane    sql.NullString
		toSession   *int64
		toPane      string
		viaID       int64
		projectID   *int64
	}
	read := func(id int64) row {
		var r row
		if err := db.QueryRow(
			`SELECT from_session_id, from_pane, to_session_id, to_pane, via_id, project_id
			 FROM session_navigations WHERE id = ?`, id,
		).Scan(&r.fromSession, &r.fromPane, &r.toSession, &r.toPane, &r.viaID, &r.projectID); err != nil {
			t.Fatalf("read nav row %d: %v", id, err)
		}
		return r
	}

	r1 := read(id1)
	if r1.fromSession != nil || r1.fromPane.Valid {
		t.Errorf("first row from_* = (%v,%v); want NULL/NULL", r1.fromSession, r1.fromPane)
	}
	if r1.toSession == nil || *r1.toSession != s1 {
		t.Errorf("first row to_session_id = %v; want %d", r1.toSession, s1)
	}
	if r1.toPane != "%1" {
		t.Errorf("first row to_pane = %q; want %%1", r1.toPane)
	}
	if r1.viaID != int64(navvia.NavViaManual) {
		t.Errorf("first row via_id = %d; want %d", r1.viaID, navvia.NavViaManual)
	}
	if r1.projectID == nil || *r1.projectID != 1 {
		t.Errorf("first row project_id = %v; want 1", r1.projectID)
	}

	r2 := read(id2)
	if r2.fromSession == nil || *r2.fromSession != s1 {
		t.Errorf("second row from_session_id = %v; want %d (prior destination)", r2.fromSession, s1)
	}
	if !r2.fromPane.Valid || r2.fromPane.String != "%1" {
		t.Errorf("second row from_pane = %v; want %%1", r2.fromPane)
	}
	if r2.toSession == nil || *r2.toSession != s2 {
		t.Errorf("second row to_session_id = %v; want %d", r2.toSession, s2)
	}
	if r2.viaID != int64(navvia.NavViaGoto) {
		t.Errorf("second row via_id = %d; want %d", r2.viaID, navvia.NavViaGoto)
	}
}

// TestRecordNav_NoOpOnSamePane confirms a move to the pane the client already
// occupies inserts nothing (collapses the paired focus-change hooks).
func TestRecordNav_NoOpOnSamePane(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj", "/tmp/proj")
	seedNavSession(t, db, "uuid-1", 1, "%1", 0, "")

	if _, err := RecordNav("client-a", "%1", navvia.NavViaManual); err != nil {
		t.Fatalf("RecordNav first: %v", err)
	}
	id, err := RecordNav("client-a", "%1", navvia.NavViaManual)
	if err != nil {
		t.Fatalf("RecordNav repeat: %v", err)
	}
	if id != 0 {
		t.Errorf("repeat RecordNav returned id %d; want 0 (no-op)", id)
	}

	var count int
	if err := db.QueryRow(
		"SELECT count(*) FROM session_navigations WHERE client = ?", "client-a",
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d; want 1 (the no-op did not insert)", count)
	}
}

// TestRecordNav_UntrackedDestination stores the raw pane with a NULL session id
// when the destination pane hosts no tracked session.
func TestRecordNav_UntrackedDestination(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj", "/tmp/proj")

	id, err := RecordNav("client-a", "%99", navvia.NavViaManual)
	if err != nil {
		t.Fatalf("RecordNav: %v", err)
	}
	var toSession *int64
	var toPane string
	var projectID *int64
	if err := db.QueryRow(
		"SELECT to_session_id, to_pane, project_id FROM session_navigations WHERE id = ?", id,
	).Scan(&toSession, &toPane, &projectID); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if toSession != nil {
		t.Errorf("to_session_id = %v; want NULL (untracked pane)", *toSession)
	}
	if toPane != "%99" {
		t.Errorf("to_pane = %q; want %%99", toPane)
	}
	if projectID != nil {
		t.Errorf("project_id = %v; want NULL (untracked pane)", *projectID)
	}
}

// TestListNavTrail_NewestFirstAndScoping confirms the reader orders newest-first,
// joins endpoint task ids + summary, and scopes by client (empty = all).
func TestListNavTrail_NewestFirstAndScoping(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj", "/tmp/proj")
	seedNavTask(t, db, 100, 1, "Task 100")
	seedNavSession(t, db, "uuid-1", 1, "%1", 100, "summary for 100")
	seedNavSession(t, db, "uuid-2", 1, "%2", 0, "")

	if _, err := RecordNav("client-a", "%1", navvia.NavViaManual); err != nil {
		t.Fatalf("nav a1: %v", err)
	}
	if _, err := RecordNav("client-a", "%2", navvia.NavViaGoto); err != nil {
		t.Fatalf("nav a2: %v", err)
	}
	if _, err := RecordNav("client-b", "%1", navvia.NavViaManual); err != nil {
		t.Fatalf("nav b1: %v", err)
	}

	scoped, err := ListNavTrail("client-a", 50)
	if err != nil {
		t.Fatalf("ListNavTrail client-a: %v", err)
	}
	if len(scoped) != 2 {
		t.Fatalf("client-a trail len = %d; want 2", len(scoped))
	}
	// Newest first: the %2 (goto) move precedes the %1 move.
	if scoped[0].ToPane != "%2" || scoped[0].Via != "goto" {
		t.Errorf("newest edge = (%q,%q); want (%%2,goto)", scoped[0].ToPane, scoped[0].Via)
	}
	if scoped[1].ToPane != "%1" || scoped[1].Via != "manual" {
		t.Errorf("oldest edge = (%q,%q); want (%%1,manual)", scoped[1].ToPane, scoped[1].Via)
	}
	// The %1 destination carries task 100 + its summary.
	if scoped[1].ToTaskID == nil || *scoped[1].ToTaskID != 100 {
		t.Errorf("to_task_id = %v; want 100", scoped[1].ToTaskID)
	}
	if scoped[1].ToSummary != "summary for 100" {
		t.Errorf("to_summary = %q; want %q", scoped[1].ToSummary, "summary for 100")
	}

	all, err := ListNavTrail("", 50)
	if err != nil {
		t.Fatalf("ListNavTrail all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("all-clients trail len = %d; want 3", len(all))
	}
}
