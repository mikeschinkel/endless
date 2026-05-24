package monitor

import (
	"database/sql"
	"strings"
	"testing"
)

// projectNextTables are the five tables added by migrateV11.
var projectNextTables = []string{
	"project_next",
	"project_next_lanes",
	"project_next_items",
	"project_next_pending",
	"project_next_revisions",
}

// TestProjectNextHasIDColumns enforces the house rule that every new table
// starts with `id INTEGER PRIMARY KEY`. Uses PRAGMA table_info to confirm
// column 0 is named "id" with pk=1.
func TestProjectNextHasIDColumns(t *testing.T) {
	db := freshDB(t)
	if _, err := migrate(db, MigrateOpts{Runner: RunnerAuto, SkipBackup: true}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, table := range projectNextTables {
		rows, err := db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			t.Fatalf("PRAGMA table_info(%s): %v", table, err)
		}
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
			first   = true
			found   bool
		)
		for rows.Next() {
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				rows.Close()
				t.Fatalf("scan %s: %v", table, err)
			}
			if first {
				found = true
				if name != "id" {
					t.Errorf("%s column 0: got %q, want \"id\"", table, name)
				}
				if pk != 1 {
					t.Errorf("%s column 0: pk=%d, want 1", table, pk)
				}
				if !strings.EqualFold(ctype, "INTEGER") {
					t.Errorf("%s column 0: type=%q, want INTEGER", table, ctype)
				}
				first = false
			}
		}
		rows.Close()
		if !found {
			t.Errorf("%s: no columns reported by PRAGMA table_info", table)
		}
	}
}

// TestProjectNextCascadeDeletes inserts a row chain across the four
// cascade-linked tables (project_next, lanes, items, pending), deletes the
// projects row, and asserts each of those tables is empty.
//
// project_next_revisions is deliberately NOT inserted into in this test:
// per the E-1421 schema, its FKs have no ON DELETE clause (NO ACTION
// default). The audit trail is immutable — deleting a project_next row
// while revisions reference it is blocked by SQLite. That property is
// out of scope for the cascade test.
func TestProjectNextCascadeDeletes(t *testing.T) {
	db := freshDB(t)
	if _, err := migrate(db, MigrateOpts{Runner: RunnerAuto, SkipBackup: true}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign_keys: %v", err)
	}

	var projectID, projectNextID, laneID int64

	res, err := db.Exec(
		"INSERT INTO projects (name, path) VALUES (?, ?)",
		"cascade-test", "/tmp/cascade-test",
	)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	projectID, _ = res.LastInsertId()

	res, err = db.Exec("INSERT INTO project_next (project_id) VALUES (?)", projectID)
	if err != nil {
		t.Fatalf("insert project_next: %v", err)
	}
	projectNextID, _ = res.LastInsertId()

	res, err = db.Exec(
		"INSERT INTO project_next_lanes (project_next_id, lane_id, priority, rationale) VALUES (?, ?, ?, ?)",
		projectNextID, "lane-a", 1, "test rationale",
	)
	if err != nil {
		t.Fatalf("insert lane: %v", err)
	}
	laneID, _ = res.LastInsertId()

	if _, err := db.Exec(
		"INSERT INTO project_next_items (project_next_lane_id, task_id, reason, position) VALUES (?, ?, ?, ?)",
		laneID, "E-9999", "test reason", 0,
	); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	if _, err := db.Exec(
		"INSERT INTO project_next_pending (project_next_id, task_id, reason) VALUES (?, ?, ?)",
		projectNextID, "E-9998", "auto-added test",
	); err != nil {
		t.Fatalf("insert pending: %v", err)
	}

	if _, err := db.Exec("DELETE FROM projects WHERE id = ?", projectID); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	cascadingTables := []string{
		"project_next",
		"project_next_lanes",
		"project_next_items",
		"project_next_pending",
	}
	for _, table := range cascadingTables {
		var n int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows after cascade delete; want 0", table, n)
		}
	}
}

// TestProjectNextLaneUniqueRejectsDuplicate verifies that two lanes with the
// same (project_next_id, lane_id) tuple are rejected by the UNIQUE constraint.
func TestProjectNextLaneUniqueRejectsDuplicate(t *testing.T) {
	db := freshDB(t)
	if _, err := migrate(db, MigrateOpts{Runner: RunnerAuto, SkipBackup: true}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	res, err := db.Exec("INSERT INTO projects (name, path) VALUES (?, ?)", "p", "/tmp/p")
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	pid, _ := res.LastInsertId()
	res, err = db.Exec("INSERT INTO project_next (project_id) VALUES (?)", pid)
	if err != nil {
		t.Fatalf("insert project_next: %v", err)
	}
	pnid, _ := res.LastInsertId()

	if _, err := db.Exec(
		"INSERT INTO project_next_lanes (project_next_id, lane_id, priority, rationale) VALUES (?, ?, ?, ?)",
		pnid, "dup-lane", 1, "r1",
	); err != nil {
		t.Fatalf("first lane insert: %v", err)
	}
	_, err = db.Exec(
		"INSERT INTO project_next_lanes (project_next_id, lane_id, priority, rationale) VALUES (?, ?, ?, ?)",
		pnid, "dup-lane", 2, "r2",
	)
	if err == nil {
		t.Fatal("duplicate (project_next_id, lane_id) insert succeeded; want UNIQUE error")
	}
	if !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("error %q does not mention UNIQUE", err.Error())
	}
}

// TestProjectNextItemPositionUniqueRejectsDuplicate verifies that two items
// with the same (project_next_lane_id, position) tuple are rejected.
func TestProjectNextItemPositionUniqueRejectsDuplicate(t *testing.T) {
	db := freshDB(t)
	if _, err := migrate(db, MigrateOpts{Runner: RunnerAuto, SkipBackup: true}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	res, err := db.Exec("INSERT INTO projects (name, path) VALUES (?, ?)", "p", "/tmp/p")
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	pid, _ := res.LastInsertId()
	res, err = db.Exec("INSERT INTO project_next (project_id) VALUES (?)", pid)
	if err != nil {
		t.Fatalf("insert project_next: %v", err)
	}
	pnid, _ := res.LastInsertId()
	res, err = db.Exec(
		"INSERT INTO project_next_lanes (project_next_id, lane_id, priority, rationale) VALUES (?, ?, ?, ?)",
		pnid, "lane-x", 1, "r",
	)
	if err != nil {
		t.Fatalf("insert lane: %v", err)
	}
	laneID, _ := res.LastInsertId()

	if _, err := db.Exec(
		"INSERT INTO project_next_items (project_next_lane_id, task_id, reason, position) VALUES (?, ?, ?, ?)",
		laneID, "E-1", "r1", 0,
	); err != nil {
		t.Fatalf("first item insert: %v", err)
	}
	_, err = db.Exec(
		"INSERT INTO project_next_items (project_next_lane_id, task_id, reason, position) VALUES (?, ?, ?, ?)",
		laneID, "E-2", "r2", 0,
	)
	if err == nil {
		t.Fatal("duplicate (project_next_lane_id, position) insert succeeded; want UNIQUE error")
	}
	if !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("error %q does not mention UNIQUE", err.Error())
	}
}

// TestProjectNextItemFKRejectsOrphan verifies that inserting an item with a
// nonexistent project_next_lane_id is rejected by the FK constraint. Confirms
// PRAGMA foreign_keys is ON before testing so a disabled-FK regression fails
// loudly rather than silently passing.
func TestProjectNextItemFKRejectsOrphan(t *testing.T) {
	db := freshDB(t)
	if _, err := migrate(db, MigrateOpts{Runner: RunnerAuto, SkipBackup: true}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign_keys: %v", err)
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("read PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d; want 1 (enforcement must be on for this test)", fk)
	}

	_, err := db.Exec(
		"INSERT INTO project_next_items (project_next_lane_id, task_id, reason, position) VALUES (?, ?, ?, ?)",
		99999, "E-orphan", "r", 0,
	)
	if err == nil {
		t.Fatal("insert with nonexistent project_next_lane_id succeeded; want FK error")
	}
	if !strings.Contains(err.Error(), "FOREIGN KEY") && !strings.Contains(err.Error(), "foreign key") {
		t.Errorf("error %q does not mention FOREIGN KEY", err.Error())
	}
}
