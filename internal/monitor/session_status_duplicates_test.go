package monitor

import (
	"database/sql"
	"testing"
)

// snDuplicates records `dupe duplicates keeper` in its stored active-voice
// form: source=dupe, target=keeper, dep_type='duplicates'.
func snDuplicates(t *testing.T, db *sql.DB, dupeID, keeperID int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		 VALUES ('task', ?, 'task', ?, 'duplicates')`,
		dupeID, keeperID,
	); err != nil {
		t.Fatalf("snDuplicates %d duplicates %d: %v", dupeID, keeperID, err)
	}
}

// TestSessionStatusRows_Duplicates drives the E-1185 column through the focal
// query. The assertion that matters is the ENDPOINT: the note belongs to the
// task that gets closed, which for `duplicates` is the relation's SOURCE — the
// opposite end from `replaces`. Get it backwards and the annotation lands on
// the task that is still being worked, which is the one row it must never
// appear on.
func TestSessionStatusRows_Duplicates(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, dupe, keeper, unrelated = 200, 201, 202, 203

	snTask(t, db, focal, 1, "underway", "now", "")
	snTask(t, db, dupe, 1, "obsolete", "now", "")
	snTask(t, db, keeper, 1, "underway", "now", "")
	snTask(t, db, unrelated, 1, "confirmed", "now", "")

	snSession(t, db, 1, 1, focal, "working")
	snSessionTask(t, db, 1, focal)
	snSessionTask(t, db, 1, dupe)
	snSessionTask(t, db, 1, keeper)
	snSessionTask(t, db, 1, unrelated)

	snDuplicates(t, db, dupe, keeper)

	rows, err := SessionStatusRows(focal, 0, true)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}

	r, ok := snRowByID(rows, dupe)
	if !ok {
		t.Fatalf("dupe row missing")
	}
	if len(r.Duplicates) != 1 || r.Duplicates[0] != keeper {
		t.Errorf("dupe Duplicates = %v, want [%d]", r.Duplicates, keeper)
	}

	// The keeper is the task still worth doing; it is duplicated BY something,
	// which this column does not carry.
	if r, ok := snRowByID(rows, keeper); !ok || len(r.Duplicates) != 0 {
		t.Errorf("keeper Duplicates = %v, want empty", r.Duplicates)
	}
	if r, ok := snRowByID(rows, unrelated); !ok || len(r.Duplicates) != 0 {
		t.Errorf("unrelated Duplicates = %v, want empty", r.Duplicates)
	}
}

// TestSessionStatusRows_DuplicatesIsNotReplacedBy: the two columns are mirror
// images over the same table, so a swapped column in either expression would
// still return plausible ids. Seeding one relation and asserting the OTHER
// column stays empty is what catches that.
func TestSessionStatusRows_DuplicatesIsNotReplacedBy(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, dupe, keeper = 300, 301, 302

	snTask(t, db, focal, 1, "underway", "now", "")
	snTask(t, db, dupe, 1, "obsolete", "now", "")
	snTask(t, db, keeper, 1, "underway", "now", "")

	snSession(t, db, 1, 1, focal, "working")
	snSessionTask(t, db, 1, focal)
	snSessionTask(t, db, 1, dupe)
	snSessionTask(t, db, 1, keeper)

	snDuplicates(t, db, dupe, keeper)

	rows, err := SessionStatusRows(focal, 0, true)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}
	for _, id := range []int64{dupe, keeper} {
		r, ok := snRowByID(rows, id)
		if !ok {
			t.Fatalf("row %d missing", id)
		}
		if len(r.ReplacedBy) != 0 {
			t.Errorf("row %d ReplacedBy = %v, want empty — a duplicates row is not a replaces row",
				id, r.ReplacedBy)
		}
	}
}

// TestSessionStatusRows_DuplicatesMultipleAndRemoved pins the group_concat path
// and the live_tasks join in one pass: two keepers come back as two ids, and a
// removed keeper is never named.
func TestSessionStatusRows_DuplicatesMultipleAndRemoved(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, dupe, keeperA, keeperB, gone = 400, 401, 402, 403, 404

	snTask(t, db, focal, 1, "underway", "now", "")
	snTask(t, db, dupe, 1, "obsolete", "now", "")
	snTask(t, db, keeperA, 1, "underway", "now", "")
	snTask(t, db, keeperB, 1, "ready", "now", "")

	snSession(t, db, 1, 1, focal, "working")
	snSessionTask(t, db, 1, focal)
	snSessionTask(t, db, 1, dupe)

	snDuplicates(t, db, dupe, keeperA)
	snDuplicates(t, db, dupe, keeperB)
	// `gone` has no tasks row at all — the shape a removal leaves behind.
	snDuplicates(t, db, dupe, gone)

	rows, err := SessionStatusRows(focal, 0, true)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}
	r, ok := snRowByID(rows, dupe)
	if !ok {
		t.Fatalf("dupe row missing")
	}
	if len(r.Duplicates) != 2 {
		t.Fatalf("dupe Duplicates = %v, want 2 ids (the removed one excluded)", r.Duplicates)
	}
	for _, id := range r.Duplicates {
		if id == gone {
			t.Errorf("Duplicates named the removed task E-%d", gone)
		}
	}
}

// TestSessionStatusRowsForSession_Duplicates: the second query selects the same
// column list, so the column has to be there too.
func TestSessionStatusRowsForSession_Duplicates(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const dupe, keeper = 500, 501

	snTask(t, db, dupe, 1, "obsolete", "now", "")
	snTask(t, db, keeper, 1, "underway", "now", "")
	snSession(t, db, 1, 1, keeper, "working")
	// This query filters to surfaced(2)/revisited(3) rows.
	snSessionTaskRel(t, db, 1, dupe, 2)

	snDuplicates(t, db, dupe, keeper)

	rows, err := SessionStatusRowsForSession(1, true)
	if err != nil {
		t.Fatalf("SessionStatusRowsForSession: %v", err)
	}
	r, ok := snRowByID(rows, dupe)
	if !ok {
		t.Fatalf("dupe row missing")
	}
	if len(r.Duplicates) != 1 || r.Duplicates[0] != keeper {
		t.Errorf("dupe Duplicates = %v, want [%d]", r.Duplicates, keeper)
	}
}
