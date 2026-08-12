package monitor

import (
	"database/sql"
	"testing"
)

// snReplaces records `old replaced_by new` in its stored active-voice form:
// source=new, target=old, dep_type='replaces'.
func snReplaces(t *testing.T, db *sql.DB, newID, oldID int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		 VALUES ('task', ?, 'task', ?, 'replaces')`,
		newID, oldID,
	); err != nil {
		t.Fatalf("snReplaces %d replaces %d: %v", newID, oldID, err)
	}
}

// TestSessionStatusRows_ReplacedBy drives the E-1956 column through the focal
// query: the replaced task carries its replacement's id, the replacement itself
// carries nothing (the relation is directional), and an unrelated row is empty.
func TestSessionStatusRows_ReplacedBy(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, superseded, replacement, unrelated = 100, 101, 102, 103

	snTask(t, db, focal, 1, "underway", "now", "")
	snTask(t, db, superseded, 1, "assumed", "now", "")
	snTask(t, db, replacement, 1, "underway", "now", "")
	snTask(t, db, unrelated, 1, "confirmed", "now", "")

	snSession(t, db, 1, 1, focal, "working")
	snSessionTask(t, db, 1, focal)
	snSessionTask(t, db, 1, superseded)
	snSessionTask(t, db, 1, replacement)
	snSessionTask(t, db, 1, unrelated)

	snReplaces(t, db, replacement, superseded)

	rows, err := SessionStatusRows(focal, 0, true)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}

	r, ok := snRowByID(rows, superseded)
	if !ok {
		t.Fatalf("superseded row missing")
	}
	if len(r.ReplacedBy) != 1 || r.ReplacedBy[0] != replacement {
		t.Errorf("superseded ReplacedBy = %v, want [%d]", r.ReplacedBy, replacement)
	}

	// Directional: the replacement replaces something, it is not replaced.
	if r, ok := snRowByID(rows, replacement); !ok || len(r.ReplacedBy) != 0 {
		t.Errorf("replacement ReplacedBy = %v, want empty", r.ReplacedBy)
	}
	if r, ok := snRowByID(rows, unrelated); !ok || len(r.ReplacedBy) != 0 {
		t.Errorf("unrelated ReplacedBy = %v, want empty", r.ReplacedBy)
	}
}

// TestSessionStatusRows_ReplacedByMultiple pins the group_concat path: two
// replacements come back as two ids, not one mangled string.
func TestSessionStatusRows_ReplacedByMultiple(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, a, b = 100, 201, 202

	snTask(t, db, focal, 1, "assumed", "now", "")
	snTask(t, db, a, 1, "underway", "now", "")
	snTask(t, db, b, 1, "underway", "now", "")
	snSession(t, db, 1, 1, focal, "working")

	snReplaces(t, db, a, focal)
	snReplaces(t, db, b, focal)

	rows, err := SessionStatusRows(focal, 0, true)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}
	r, ok := snRowByID(rows, focal)
	if !ok {
		t.Fatalf("focal row missing")
	}
	if len(r.ReplacedBy) != 2 || r.ReplacedBy[0] != a || r.ReplacedBy[1] != b {
		t.Errorf("ReplacedBy = %v, want [%d %d]", r.ReplacedBy, a, b)
	}
}

// TestSessionStatusRows_ReplacedByIgnoresRemoved: `task remove` retains the row,
// and a retained id must not be named as a live successor.
func TestSessionStatusRows_ReplacedByIgnoresRemoved(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, replacement = 100, 101

	snTask(t, db, focal, 1, "assumed", "now", "")
	snTask(t, db, replacement, 1, "underway", "now", "")
	snSession(t, db, 1, 1, focal, "working")
	snReplaces(t, db, replacement, focal)

	if _, err := db.Exec(`UPDATE tasks SET removed = 1 WHERE id = ?`, replacement); err != nil {
		t.Fatalf("remove replacement: %v", err)
	}

	rows, err := SessionStatusRows(focal, 0, true)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}
	r, _ := snRowByID(rows, focal)
	if len(r.ReplacedBy) != 0 {
		t.Errorf("ReplacedBy = %v, want empty (replacement was removed)", r.ReplacedBy)
	}
}

// TestSessionStatusRowsForSession_ReplacedBy: the no-goal view reads the same
// column. Both queries select one shared column list through one shared scanner,
// and this is the assertion that they actually agree.
func TestSessionStatusRowsForSession_ReplacedBy(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const superseded, replacement = 101, 102

	snTask(t, db, superseded, 1, "assumed", "now", "")
	snTask(t, db, replacement, 1, "underway", "now", "")
	snSession(t, db, 1, 1, 0, "working")
	snSessionTaskRel(t, db, 1, superseded, 2) // surfaced
	snReplaces(t, db, replacement, superseded)

	rows, err := SessionStatusRowsForSession(1, true)
	if err != nil {
		t.Fatalf("SessionStatusRowsForSession: %v", err)
	}
	r, ok := snRowByID(rows, superseded)
	if !ok {
		t.Fatalf("superseded row missing")
	}
	if len(r.ReplacedBy) != 1 || r.ReplacedBy[0] != replacement {
		t.Errorf("ReplacedBy = %v, want [%d]", r.ReplacedBy, replacement)
	}
}

func TestParseReplacedBy(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []int64
	}{
		{"empty", "", nil},
		{"single", "42", []int64{42}},
		{"multiple", "42,43", []int64{42, 43}},
		{"spaced", " 42 , 43 ", []int64{42, 43}},
		// An annotation column must never cost the caller its whole row set, so
		// a garbage element is dropped rather than raised.
		{"skips garbage", "42,nope,43", []int64{42, 43}},
		{"all garbage", "nope", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseReplacedBy(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("parseReplacedBy(%q) = %v, want %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("parseReplacedBy(%q) = %v, want %v", c.in, got, c.want)
				}
			}
		})
	}
}
