package monitor

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/sessionstate"
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

// TestProjectStatusTaskStatusesComesFromTheVocabulary pins that the status set
// is DERIVED from taskstatus.AwaitsUser rather than spelled out. A status list
// inside a SQL string is invisible to every tool, which is exactly why it rots.
func TestProjectStatusTaskStatusesComesFromTheVocabulary(t *testing.T) {
	base := projectStatusTaskStatuses(false)
	for _, want := range []string{"'unverified'", "'unreviewed'", "'submitted'", "'underway'"} {
		if !strings.Contains(base, want) {
			t.Errorf("projectStatusTaskStatuses(false) omits %s: %s", want, base)
		}
	}
	// `ready` is spawnable work — a claim on capacity, not on attention — so it
	// is off the default view and reachable only through --all.
	if strings.Contains(base, "'ready'") {
		t.Errorf("projectStatusTaskStatuses(false) includes 'ready': %s", base)
	}
	if !strings.Contains(projectStatusTaskStatuses(true), "'ready'") {
		t.Errorf("projectStatusTaskStatuses(true) omits 'ready': %s", projectStatusTaskStatuses(true))
	}
}

// seedProjectStatusTask inserts one task for these queries to find.
func seedProjectStatusTask(t *testing.T, db *sql.DB, id, projectID int64, status string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, status, phase, type_id, updated_at)
		 VALUES (?, ?, ?, ?, 'now', 1, '2026-08-30T10:00:00')`,
		id, projectID, "task "+status, status,
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

// TestProjectStatusRowsMergesASessionWithItsTask is the defining join: a
// session working E-1 and the task E-1 are ONE row, not two lines saying the
// same thing twice.
func TestProjectStatusRowsMergesASessionWithItsTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedProjectStatusTask(t, db, 1, 1, "underway")
	taskID := int64(1)
	seedProjectStatusSession(t, db, 10, 1, "working", &taskID)

	rows, err := ProjectStatusRows(1, false)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 merged row: %+v", len(rows), rows)
	}
	r := rows[0]
	if !r.HasTask() || !r.HasSession() {
		t.Fatalf("the merged row lost a half: %+v", r)
	}
	if r.TaskID != 1 || r.SessionID != 10 {
		t.Errorf("merged row = task %d / session %d, want 1 / 10", r.TaskID, r.SessionID)
	}
	if r.TypeSlug != "todo" {
		t.Errorf("merged row lost its task type: %q", r.TypeSlug)
	}
}

// TestProjectStatusRowsSelectsAttentionStatuses pins which task statuses earn a
// row of their own, and which do not.
func TestProjectStatusRowsSelectsAttentionStatuses(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	for i, status := range []string{
		"unverified", "unreviewed", "submitted", "underway", // included
		"ready",                             // --all only
		"confirmed", "untriaged", "revisit", // never
	} {
		seedProjectStatusTask(t, db, int64(i+1), 1, status)
	}

	got := statusSet(t, 1, false)
	for _, want := range []string{"unverified", "unreviewed", "submitted", "underway"} {
		if !got[want] {
			t.Errorf("the default set omits %q", want)
		}
	}
	for _, unwanted := range []string{"ready", "confirmed", "untriaged", "revisit"} {
		if got[unwanted] {
			t.Errorf("the default set includes %q", unwanted)
		}
	}

	all := statusSet(t, 1, true)
	if !all["ready"] {
		t.Errorf("--all omits 'ready'")
	}
	if all["confirmed"] || all["untriaged"] || all["revisit"] {
		t.Errorf("--all leaked a status that claims nothing: %v", all)
	}
}

func statusSet(t *testing.T, projectID int64, all bool) map[string]bool {
	t.Helper()
	rows, err := ProjectStatusRows(projectID, all)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	out := map[string]bool{}
	for _, r := range rows {
		out[r.Status] = true
	}
	return out
}

// TestProjectStatusRowsIsEveryLiveSession reverses the deliberate omission
// E-1976 shipped with (E-2091).
//
// That omission excluded 'needs_input' because nothing transitioned a session
// INTO it, so every row carrying it was a session that registered and never had
// a turn — 34 in one project, none on a pane that still existed. Hiding them is
// how they rotted unseen. `project status` caps each rank at ten rows and names the
// remainder in a footer, which is machinery built for exactly this, and a row on
// the row is what provides the mechanism to resolve it. So the filter is
// sessionstate.Live and nothing narrower: every live state earns a row, 'ended'
// alone does not.
func TestProjectStatusRowsIsEveryLiveSession(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedProjectStatusSession(t, db, 12, 1, "ended", nil)

	for i, state := range sessionstate.Get(sessionstate.Live) {
		seedProjectStatusSession(t, db, int64(20+i), 1, state, nil)
	}

	rows, err := ProjectStatusRows(1, false)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.SessionState] = true
	}
	for _, state := range sessionstate.Get(sessionstate.Live) {
		if !got[state] {
			t.Errorf("a %q session is missing from the row set", state)
		}
	}
	if got[sessionstate.Ended] {
		t.Errorf("an ended session rendered in the row set")
	}
	if len(rows) != len(sessionstate.Get(sessionstate.Live)) {
		t.Errorf("the query returned %d rows, want one per live state (%d): %+v",
			len(rows), len(sessionstate.Get(sessionstate.Live)), rows)
	}
}

// TestProjectStatusRowsExcludesHiddenSessions: `endless session hide` is how a
// user says "stop showing me this one", and a view whose whole job is attention
// triage is the last surface that should ignore it.
func TestProjectStatusRowsExcludesHiddenSessions(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedProjectStatusSession(t, db, 10, 1, "idle", nil)
	if _, err := db.Exec("UPDATE sessions SET hidden = 1 WHERE id = 10"); err != nil {
		t.Fatalf("hide session: %v", err)
	}
	rows, err := ProjectStatusRows(1, false)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a hidden session rendered in the row set: %+v", rows)
	}
}

// TestProjectStatusRowsExcludesDeadSessions pins the liveness filter, on the
// same terms ListLiveSessions applies it: `dead` means we REACHED the session's
// tmux server and its pane was not there. `unknown` — server unreachable — stays,
// because "could not disprove" is not "gone" (E-1898).
func TestProjectStatusRowsExcludesDeadSessions(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedProjectStatusSession(t, db, 10, 1, "idle", nil)
	seedProjectStatusSession(t, db, 11, 1, "idle", nil)
	bindSessionPane(t, db, 10, "%1")
	bindSessionPane(t, db, 11, "%2")

	// A reachable server carrying only %1: session 10 is live, 11 is dead.
	restore := SetTestTmuxObservation(TestServerUUID, map[string]string{"%1": "claude"})
	defer restore()

	rows, err := ProjectStatusRows(1, false)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	if len(rows) != 1 || rows[0].SessionID != 10 {
		t.Fatalf("sessions = %+v, want only the live session (10)", rows)
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

	rows, err := ProjectStatusRows(1, false)
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
	rows, err := ProjectStatusRows(1, false)
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
