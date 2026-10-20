package monitor

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// E-2405 (P15): a session that spawns a task keeps it on its own `session
// status` board, labeled Spawned, until the task settles — and the spawned task
// may live in a different project from the spawner. These tests seed the
// spawner and the spawned task in DIFFERENT projects on purpose: nothing in
// either row query filters on the session's project, and this is what proves
// it. A project filter added later would silently drop exactly the cross-project
// delegation the relation exists to keep in view.

// spawnedFixture: project 1 holds the spawner's claimed task E-100 and session 1
// (the spawner); project 2 holds the spawned task E-200 and session 2, which
// claimed it. Session 1 records E-200 at relation spawned, as the
// session_tasks.spawned executor writes it.
func spawnedFixture(t *testing.T, spawnerTask int64) *sql.DB {
	t.Helper()
	db := withTestDB(t)
	seedProject(t, db, 1, "spawner-project", "/p1")
	seedProject(t, db, 2, "other-project", "/p2")

	snTask(t, db, 100, 1, "underway", "now", "")
	snTask(t, db, 200, 2, "underway", "now", "")

	snSession(t, db, 1, 1, spawnerTask, "working")
	if spawnerTask != 0 {
		snSessionTaskRel(t, db, 1, spawnerTask, int64(sessiontaskrelation.RelationClaimed))
	}
	snSessionTaskRel(t, db, 1, 200, int64(sessiontaskrelation.RelationSpawned))

	snSession(t, db, 2, 2, 200, "working")
	snSessionTaskRel(t, db, 2, 200, int64(sessiontaskrelation.RelationClaimed))
	return db
}

func settleTask(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	if _, err := db.Exec(`UPDATE tasks SET status = 'confirmed' WHERE id = ?`, id); err != nil {
		t.Fatalf("settle E-%d: %v", id, err)
	}
}

// TestSessionStatusRows_SpawnedTaskInAnotherProject covers the focal path — the
// spawner has claimed a task of its own, which is the normal case for a session
// that spawns.
func TestSessionStatusRows_SpawnedTaskInAnotherProject(t *testing.T) {
	db := spawnedFixture(t, 100)

	rows, err := SessionStatusRows(100, 0, false)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}
	if err := AnnotateSessionStatusRelation(rows, 1); err != nil {
		t.Fatalf("AnnotateSessionStatusRelation: %v", err)
	}
	r, ok := snRowByID(rows, 200)
	if !ok {
		t.Fatalf("spawned task E-200 (project 2) missing from the spawner's board (project 1): %+v", rows)
	}
	if r.ProjectID != 2 {
		t.Errorf("E-200 project = %d, want 2 (the fixture must be cross-project)", r.ProjectID)
	}
	if r.Relation != sessiontaskrelation.RelationSpawned || r.Relation.Label() != "Spawned" {
		t.Errorf("E-200 relation = %v (%q), want Spawned", r.Relation, r.Relation.Label())
	}

	settleTask(t, db, 200)
	rows, err = SessionStatusRows(100, 0, false)
	if err != nil {
		t.Fatalf("SessionStatusRows after settle: %v", err)
	}
	if _, ok := snRowByID(rows, 200); ok {
		t.Errorf("settled spawned task E-200 still on the spawner's board")
	}
}

// TestSessionStatusRowsForSession_SpawnedTaskInAnotherProject covers the no-goal
// path: a spawner with no claimed task still sees what it spawned, because
// nonClaimedRelationIDs is built from the enum and so includes spawned.
func TestSessionStatusRowsForSession_SpawnedTaskInAnotherProject(t *testing.T) {
	db := spawnedFixture(t, 0)

	rows, err := SessionStatusRowsForSession(1, false)
	if err != nil {
		t.Fatalf("SessionStatusRowsForSession: %v", err)
	}
	if err := AnnotateSessionStatusRelation(rows, 1); err != nil {
		t.Fatalf("AnnotateSessionStatusRelation: %v", err)
	}
	r, ok := snRowByID(rows, 200)
	if !ok {
		t.Fatalf("spawned task E-200 missing from the no-goal spawner's board: %+v", rows)
	}
	if r.Relation != sessiontaskrelation.RelationSpawned {
		t.Errorf("E-200 relation = %v, want spawned", r.Relation)
	}

	settleTask(t, db, 200)
	rows, err = SessionStatusRowsForSession(1, false)
	if err != nil {
		t.Fatalf("SessionStatusRowsForSession after settle: %v", err)
	}
	if _, ok := snRowByID(rows, 200); ok {
		t.Errorf("settled spawned task E-200 still on the no-goal spawner's board")
	}
}
