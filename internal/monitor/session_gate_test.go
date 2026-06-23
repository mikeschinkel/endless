package monitor

import (
	"database/sql"
	"fmt"
	"testing"
)

// insertTaskFull seeds a task with explicit parent_id (0 => NULL), type_id, and
// status so the revisit-ancestry walk can be exercised. epic = type_id 4.
func insertTaskFull(t *testing.T, db *sql.DB, id, projectID, parentID, typeID int64, status string) {
	t.Helper()
	var parent any
	if parentID != 0 {
		parent = parentID
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, parent_id, title, status, type_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, projectID, parent, "t", status, typeID,
	); err != nil {
		t.Fatalf("insert task id=%d: %v", id, err)
	}
}

// insertSessionWithID seeds a session row with an explicit id so session_gates
// rows can be tied to it. Returns the id.
func insertSessionWithID(t *testing.T, db *sql.DB, id, projectID int64) int64 {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, platform, state, started_at, last_activity)
		 VALUES (?, ?, ?, 'claude', 'working', '2026-06-20T00:00:00', '2026-06-20T00:00:00')`,
		id, fmt.Sprintf("uuid-%d", id), projectID,
	); err != nil {
		t.Fatalf("insert session id=%d: %v", id, err)
	}
	return id
}

const (
	typeTask = int64(1)
	typeEpic = int64(4)
)

func TestNearestRevisitEpicAncestor_NoEpicAncestor(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	insertTaskFull(t, db, 10, 1, 0, typeTask, "ready")  // root task
	insertTaskFull(t, db, 11, 1, 10, typeTask, "ready") // leaf

	_, found, err := NearestRevisitEpicAncestor(11)
	if err != nil {
		t.Fatalf("NearestRevisitEpicAncestor: %v", err)
	}
	if found {
		t.Errorf("found = true, want false (no epic in ancestry)")
	}
}

func TestNearestRevisitEpicAncestor_EpicNotInRevisit(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	insertTaskFull(t, db, 10, 1, 0, typeEpic, "in_progress") // epic, NOT revisit
	insertTaskFull(t, db, 11, 1, 10, typeTask, "ready")

	_, found, err := NearestRevisitEpicAncestor(11)
	if err != nil {
		t.Fatalf("NearestRevisitEpicAncestor: %v", err)
	}
	if found {
		t.Errorf("found = true, want false (epic ancestor not in revisit)")
	}
}

func TestNearestRevisitEpicAncestor_SingleRevisitEpic(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	insertTaskFull(t, db, 10, 1, 0, typeEpic, "revisit")
	insertTaskFull(t, db, 11, 1, 10, typeTask, "ready")
	insertTaskFull(t, db, 12, 1, 11, typeTask, "ready") // leaf

	epicID, found, err := NearestRevisitEpicAncestor(12)
	if err != nil {
		t.Fatalf("NearestRevisitEpicAncestor: %v", err)
	}
	if !found || epicID != 10 {
		t.Errorf("got (epic=%d, found=%v), want (10, true)", epicID, found)
	}
}

func TestNearestRevisitEpicAncestor_NearestWins(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	insertTaskFull(t, db, 10, 1, 0, typeEpic, "revisit")  // far epic
	insertTaskFull(t, db, 11, 1, 10, typeEpic, "revisit") // near epic
	insertTaskFull(t, db, 12, 1, 11, typeTask, "ready")   // leaf

	epicID, found, err := NearestRevisitEpicAncestor(12)
	if err != nil {
		t.Fatalf("NearestRevisitEpicAncestor: %v", err)
	}
	if !found || epicID != 11 {
		t.Errorf("got (epic=%d, found=%v), want (11, true) — nearest revisit epic", epicID, found)
	}
}

func TestNearestRevisitEpicAncestor_CycleTerminates(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	// Two tasks pointing at each other: a malformed parent_id cycle. The depth
	// cap must terminate the walk rather than loop forever. Neither is a revisit
	// epic, so found is false.
	insertTaskFull(t, db, 20, 1, 0, typeTask, "ready")
	insertTaskFull(t, db, 21, 1, 20, typeTask, "ready")
	if _, err := db.Exec("UPDATE tasks SET parent_id=21 WHERE id=20"); err != nil {
		t.Fatalf("create cycle: %v", err)
	}

	_, found, err := NearestRevisitEpicAncestor(21)
	if err != nil {
		t.Fatalf("NearestRevisitEpicAncestor (cycle): %v", err)
	}
	if found {
		t.Errorf("found = true, want false (cycle, no revisit epic)")
	}
}

func TestRevisitGate_SetPendingClearRoundTrip(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	insertTaskFull(t, db, 10, 1, 0, typeEpic, "revisit")
	sid := insertSessionWithID(t, db, 100, 1)

	if _, found, _ := PendingRevisitGate(sid); found {
		t.Fatalf("PendingRevisitGate before set: found=true, want false")
	}

	if err := SetRevisitGate(sid, 10); err != nil {
		t.Fatalf("SetRevisitGate: %v", err)
	}
	epicID, found, err := PendingRevisitGate(sid)
	if err != nil {
		t.Fatalf("PendingRevisitGate: %v", err)
	}
	if !found || epicID != 10 {
		t.Errorf("PendingRevisitGate = (%d, %v), want (10, true)", epicID, found)
	}

	cleared, err := ClearRevisitGate(sid, "revisit_continue")
	if err != nil {
		t.Fatalf("ClearRevisitGate: %v", err)
	}
	if cleared != 1 {
		t.Errorf("cleared = %d, want 1", cleared)
	}
	if _, found, _ := PendingRevisitGate(sid); found {
		t.Errorf("PendingRevisitGate after clear: found=true, want false")
	}

	// cleared_by is recorded.
	var by string
	if err := db.QueryRow(
		"SELECT cleared_by FROM session_gates WHERE session_id=?", sid,
	).Scan(&by); err != nil {
		t.Fatalf("read cleared_by: %v", err)
	}
	if by != "revisit_continue" {
		t.Errorf("cleared_by = %q, want revisit_continue", by)
	}
}

func TestRevisitGate_SetSupersedesPriorOpen(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	insertTaskFull(t, db, 10, 1, 0, typeEpic, "revisit")
	insertTaskFull(t, db, 11, 1, 0, typeEpic, "revisit")
	sid := insertSessionWithID(t, db, 100, 1)

	if err := SetRevisitGate(sid, 10); err != nil {
		t.Fatalf("SetRevisitGate(10): %v", err)
	}
	if err := SetRevisitGate(sid, 11); err != nil {
		t.Fatalf("SetRevisitGate(11): %v", err)
	}

	// Exactly one open row, pointing at the latest epic.
	epicID, found, err := PendingRevisitGate(sid)
	if err != nil {
		t.Fatalf("PendingRevisitGate: %v", err)
	}
	if !found || epicID != 11 {
		t.Errorf("PendingRevisitGate = (%d, %v), want (11, true)", epicID, found)
	}

	var open int
	if err := db.QueryRow(
		"SELECT count(*) FROM session_gates WHERE session_id=? AND cleared_at IS NULL", sid,
	).Scan(&open); err != nil {
		t.Fatalf("count open: %v", err)
	}
	if open != 1 {
		t.Errorf("open rows = %d, want 1 (prior superseded)", open)
	}

	// The prior row was cleared with 'superseded'.
	var superseded int
	if err := db.QueryRow(
		"SELECT count(*) FROM session_gates WHERE session_id=? AND cleared_by='superseded'", sid,
	).Scan(&superseded); err != nil {
		t.Fatalf("count superseded: %v", err)
	}
	if superseded != 1 {
		t.Errorf("superseded rows = %d, want 1", superseded)
	}
}

func TestClearRevisitGate_NoOpenRowReturnsZero(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	sid := insertSessionWithID(t, db, 100, 1)

	cleared, err := ClearRevisitGate(sid, "revisit_pause")
	if err != nil {
		t.Fatalf("ClearRevisitGate: %v", err)
	}
	if cleared != 0 {
		t.Errorf("cleared = %d, want 0 (nothing pending)", cleared)
	}
}
