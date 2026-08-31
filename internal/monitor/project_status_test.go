package monitor

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
)

// The project attention board's reads (E-1976).

func TestProjectSessionNameIsTmuxSafe(t *testing.T) {
	tests := []struct{ project, want string }{
		{"endless", "endless-monitor"},
		{"go-tealeaves", "go-tealeaves-monitor"},
		// tmux forbids '.' and ':' in a session name — both are target
		// separators, so a name containing one addresses something else.
		{"my.project", "my-project-monitor"},
		{"ns:proj", "ns-proj-monitor"},
		// PRODUCT: project names are user-chosen and arrive with spaces and
		// slashes in them. None of that may be a rule the user has to know.
		{"My Cool App", "My-Cool-App-monitor"},
		{"a/b/c", "a-b-c-monitor"},
		// A leading '-' would be read as a flag by tmux, so the fold is trimmed.
		{"-weird-", "weird-monitor"},
		// Every character folded away. A board still has to be reachable.
		{"...", "project-monitor"},
		{"", "project-monitor"},
	}
	for _, tt := range tests {
		if got := ProjectSessionName(tt.project); got != tt.want {
			t.Errorf("ProjectSessionName(%q) = %q, want %q", tt.project, got, tt.want)
		}
	}
}

// TestBoardTaskStatusesComesFromTheVocabulary pins that the board's status set
// is DERIVED from taskstatus.AwaitsUser rather than spelled out. A status list
// inside a SQL string is invisible to every tool, which is exactly why it rots.
func TestBoardTaskStatusesComesFromTheVocabulary(t *testing.T) {
	base := boardTaskStatuses(false)
	for _, want := range []string{"'unverified'", "'unreviewed'", "'submitted'", "'underway'"} {
		if !strings.Contains(base, want) {
			t.Errorf("boardTaskStatuses(false) omits %s: %s", want, base)
		}
	}
	// `ready` is spawnable work — a claim on capacity, not on attention — so it
	// is off the default board and reachable only through --all.
	if strings.Contains(base, "'ready'") {
		t.Errorf("boardTaskStatuses(false) includes 'ready': %s", base)
	}
	if !strings.Contains(boardTaskStatuses(true), "'ready'") {
		t.Errorf("boardTaskStatuses(true) omits 'ready': %s", boardTaskStatuses(true))
	}
}

// seedBoardTask inserts one task for the board queries to find.
func seedBoardTask(t *testing.T, db *sql.DB, id, projectID int64, status string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, status, phase, type_id, updated_at)
		 VALUES (?, ?, ?, ?, 'now', 1, '2026-08-30T10:00:00')`,
		id, projectID, "task "+status, status,
	); err != nil {
		t.Fatalf("seed task %d: %v", id, err)
	}
}

// seedBoardSession inserts one session, optionally bound to a task.
func seedBoardSession(t *testing.T, db *sql.DB, id, projectID int64, state string, taskID *int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, task_id, started_at, last_activity)
		 VALUES (?, ?, ?, ?, ?, '2026-08-30T09:00:00', '2026-08-30T11:00:00')`,
		id, "uuid-"+state+"-"+strconv.FormatInt(id, 10), projectID, state, taskID,
	); err != nil {
		t.Fatalf("seed session %d: %v", id, err)
	}
}

// TestProjectStatusRowsMergesASessionWithItsTask is the board's defining join: a
// session working E-1 and the task E-1 are ONE row, not two lines saying the
// same thing twice.
func TestProjectStatusRowsMergesASessionWithItsTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedBoardTask(t, db, 1, 1, "underway")
	taskID := int64(1)
	seedBoardSession(t, db, 10, 1, "working", &taskID)

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
		"unverified", "unreviewed", "submitted", "underway", // on the board
		"ready",                             // --all only
		"confirmed", "untriaged", "revisit", // never
	} {
		seedBoardTask(t, db, int64(i+1), 1, status)
	}

	got := statusSet(t, 1, false)
	for _, want := range []string{"unverified", "unreviewed", "submitted", "underway"} {
		if !got[want] {
			t.Errorf("default board omits %q", want)
		}
	}
	for _, unwanted := range []string{"ready", "confirmed", "untriaged", "revisit"} {
		if got[unwanted] {
			t.Errorf("default board includes %q", unwanted)
		}
	}

	all := statusSet(t, 1, true)
	if !all["ready"] {
		t.Errorf("--all board omits 'ready'")
	}
	if all["confirmed"] || all["untriaged"] || all["revisit"] {
		t.Errorf("--all board leaked a status that claims nothing: %v", all)
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

// TestProjectStatusRowsExcludesNeedsInput pins the deliberate omission E-1976
// shipped with. Nothing transitions a session INTO 'needs_input' — it is written
// on INSERT and on the revive-an-ended-row CASE, and never again — so every row
// carrying it is a session that registered and never had a turn. Ranking those
// as the board's loudest row would make the top of every board permanent noise.
// E-2091 supplies the producer; this assertion is what should change then.
func TestProjectStatusRowsExcludesNeedsInput(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedBoardSession(t, db, 10, 1, "needs_input", nil)
	seedBoardSession(t, db, 11, 1, "idle", nil)
	seedBoardSession(t, db, 12, 1, "ended", nil)

	rows, err := ProjectStatusRows(1, false)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	if len(rows) != 1 || rows[0].SessionID != 11 {
		t.Fatalf("board sessions = %+v, want only the idle session (11)", rows)
	}
}

// TestProjectStatusRowsExcludesHiddenSessions: `endless session hide` is how a
// user says "stop showing me this one", and a board whose whole job is attention
// triage is the last surface that should ignore it.
func TestProjectStatusRowsExcludesHiddenSessions(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedBoardSession(t, db, 10, 1, "idle", nil)
	if _, err := db.Exec("UPDATE sessions SET hidden = 1 WHERE id = 10"); err != nil {
		t.Fatalf("hide session: %v", err)
	}
	rows, err := ProjectStatusRows(1, false)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a hidden session rendered on the board: %+v", rows)
	}
}

// TestProjectStatusRowsExcludesDeadSessions pins the liveness filter, on the
// same terms ListLiveSessions applies it: `dead` means we REACHED the session's
// tmux server and its pane was not there. `unknown` — server unreachable — stays,
// because "could not disprove" is not "gone" (E-1898).
func TestProjectStatusRowsExcludesDeadSessions(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedBoardSession(t, db, 10, 1, "idle", nil)
	seedBoardSession(t, db, 11, 1, "idle", nil)
	bindBoardPane(t, db, 10, "%1")
	bindBoardPane(t, db, 11, "%2")

	// A reachable server carrying only %1: session 10 is live, 11 is dead.
	restore := SetTestTmuxObservation(TestServerUUID, map[string]string{"%1": "claude"})
	defer restore()

	rows, err := ProjectStatusRows(1, false)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	if len(rows) != 1 || rows[0].SessionID != 10 {
		t.Fatalf("board sessions = %+v, want only the live session (10)", rows)
	}
}

// bindBoardPane gives a seeded session a pane binding on the test server, so the
// liveness view has something to judge.
func bindBoardPane(t *testing.T, db *sql.DB, sessionID int64, address string) {
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

// TestProjectStatusRowsScopesToOneProject: the board is project-scoped, and a
// row leaking in from another project would be a claim on attention the user
// cannot act on from here.
func TestProjectStatusRowsScopesToOneProject(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedProject(t, db, 2, "q", "/tmp/q")
	seedBoardTask(t, db, 1, 1, "unverified")
	seedBoardTask(t, db, 2, 2, "unverified")
	seedBoardSession(t, db, 10, 2, "idle", nil)

	rows, err := ProjectStatusRows(1, false)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	if len(rows) != 1 || rows[0].TaskID != 1 {
		t.Fatalf("board = %+v, want only project 1's task", rows)
	}
}

// TestProjectStatusRowsOmitsRemovedTasks: reads go through live_tasks, so a
// removed task cannot leak back onto a board (E-1929).
func TestProjectStatusRowsOmitsRemovedTasks(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedBoardTask(t, db, 1, 1, "unverified")
	seedBoardTask(t, db, 2, 1, "unverified")
	if _, err := db.Exec("UPDATE tasks SET removed = 1 WHERE id = 2"); err != nil {
		t.Fatalf("remove task: %v", err)
	}
	rows, err := ProjectStatusRows(1, false)
	if err != nil {
		t.Fatalf("ProjectStatusRows: %v", err)
	}
	if len(rows) != 1 || rows[0].TaskID != 1 {
		t.Fatalf("board = %+v, want only the live task", rows)
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
