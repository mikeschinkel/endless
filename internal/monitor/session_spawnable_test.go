package monitor

import (
	"database/sql"
	"testing"
)

// E-2204 part 5: a task whose blocker has released it, with none left holding
// it, is highlighted until someone claims it. Spawnable is computed from the row
// each draw, so these tests drive the real row queries.

func spSetStatus(t *testing.T, db *sql.DB, taskID int64, status string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, status, taskID); err != nil {
		t.Fatalf("set status t=%d: %v", taskID, err)
	}
}

func spRow(t *testing.T, focal, id int64) SessionStatusRow {
	t.Helper()
	rows, err := SessionStatusRows(focal, 0, false)
	if err != nil {
		t.Fatalf("SessionStatusRows(%d): %v", focal, err)
	}
	r, ok := snRowByID(rows, id)
	if !ok {
		t.Fatalf("E-%d missing from E-%d's board", id, focal)
	}
	return r
}

// spFixture: session 1 is working E-100, which blocks E-200.
func spFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	snTask(t, db, 100, 1, "underway", "now", "")
	snTask(t, db, 101, 1, "ready", "now", "")
	snTask(t, db, 200, 1, "ready", "now", "")
	snSession(t, db, 1, 1, 100, "working")
	snSessionTaskRel(t, db, 1, 100, 1)
	snBlocks(t, db, 100, 200)
	return db
}

func TestSpawnable_OnlyBlockerResolves(t *testing.T) {
	db := spFixture(t)
	if r := spRow(t, 100, 200); r.Spawnable() {
		t.Fatalf("while its blocker is open: %+v, want not spawnable", r)
	}
	spSetStatus(t, db, 100, "assumed")
	r := spRow(t, 100, 200)
	if !r.Spawnable() || r.ReleasedByN != 1 || r.BlockedByN != 0 {
		t.Fatalf("after its only blocker resolved: %+v, want spawnable", r)
	}
	// Nothing is consumed by drawing it: the next draw — the session's next
	// prompt included — still shows it.
	if r := spRow(t, 100, 200); !r.Spawnable() {
		t.Errorf("redraw: %+v, want still spawnable", r)
	}
}

func TestSpawnable_ClearsOnceClaimed(t *testing.T) {
	db := spFixture(t)
	spSetStatus(t, db, 100, "assumed")
	snSession(t, db, 2, 1, 200, "working")
	if r := spRow(t, 100, 200); r.Spawnable() {
		t.Errorf("with a live session on it: %+v, want not spawnable", r)
	}
	spSetStatus(t, db, 200, "underway")
	ownSetState(t, db, 2, "ended")
	if r := spRow(t, 100, 200); r.Spawnable() {
		t.Errorf("claimed (underway): %+v, want not spawnable", r)
	}
}

func TestSpawnable_AnotherBlockerStillOpen(t *testing.T) {
	db := spFixture(t)
	snBlocks(t, db, 101, 200)
	spSetStatus(t, db, 100, "assumed")
	if r := spRow(t, 100, 200); r.Spawnable() || r.ReleasedByN != 1 || r.BlockedByN != 1 {
		t.Errorf("one blocker resolved, one open: %+v, want not spawnable", r)
	}
}

// TestSpawnable_NeverBlocked: a ready task nothing ever blocked is not news.
func TestSpawnable_NeverBlocked(t *testing.T) {
	db := spFixture(t)
	snSessionTaskRel(t, db, 1, 101, 2)
	if r := spRow(t, 100, 101); r.Spawnable() {
		t.Errorf("never-blocked ready task: %+v, want not spawnable", r)
	}
}

// TestSpawnable_UnclaimedSessionView: the no-goal view's query carries the same
// released count.
func TestSpawnable_UnclaimedSessionView(t *testing.T) {
	db := spFixture(t)
	snSession(t, db, 3, 1, 0, "working")
	snSessionTaskRel(t, db, 3, 200, 2)
	spSetStatus(t, db, 100, "completed")
	rows, err := SessionStatusRowsForSession(3, false)
	if err != nil {
		t.Fatalf("SessionStatusRowsForSession: %v", err)
	}
	r, ok := snRowByID(rows, 200)
	if !ok || !r.Spawnable() {
		t.Errorf("unclaimed view: %+v (found %v), want spawnable", r, ok)
	}
}
