package monitor

import (
	"testing"
	"time"
)

// TestRecordActivity_InsertsRow pins the happy path: RecordActivity
// writes a row into the activity table with the supplied project_id,
// source, working_dir, and a non-NULL session_context JSON blob when
// the context map is non-empty.
func TestRecordActivity_InsertsRow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	ctx := map[string]string{"session_id": "sess-A", "event": "test"}
	if err := RecordActivity(1, "claude", "/tmp/wd", ctx); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}

	var (
		projectID  int64
		source     string
		workingDir string
		sessCtx    *string
	)
	err := db.QueryRow(
		"SELECT project_id, source, working_dir, session_context FROM activity WHERE project_id=?",
		1,
	).Scan(&projectID, &source, &workingDir, &sessCtx)
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if projectID != 1 {
		t.Errorf("project_id = %d, want 1", projectID)
	}
	if source != "claude" {
		t.Errorf("source = %q, want claude", source)
	}
	if workingDir != "/tmp/wd" {
		t.Errorf("working_dir = %q, want /tmp/wd", workingDir)
	}
	if sessCtx == nil {
		t.Errorf("session_context is NULL, want JSON blob")
	}
}

// TestRecordActivity_NilContextStoresNull pins the nil/empty-context
// branch: when sessionCtx is nil or empty, session_context is stored
// as NULL rather than the literal JSON null.
func TestRecordActivity_NilContextStoresNull(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := RecordActivity(1, "claude", "/tmp/wd", nil); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}
	var sessCtx *string
	err := db.QueryRow(
		"SELECT session_context FROM activity WHERE project_id=?", 1,
	).Scan(&sessCtx)
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if sessCtx != nil {
		t.Errorf("session_context = %q, want NULL", *sessCtx)
	}
}

// TestShouldThrottle_NoPreviousRunFalse pins the "no previous activity"
// branch: with no rows in the table, ShouldThrottle returns false so
// the first event always fires.
func TestShouldThrottle_NoPreviousRunFalse(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	throttled, err := ShouldThrottle(1, "claude", 60)
	if err != nil {
		t.Fatalf("ShouldThrottle: %v", err)
	}
	if throttled {
		t.Errorf("ShouldThrottle with no history = true, want false")
	}
}

// TestShouldThrottle_WithinIntervalTrue pins the in-interval branch: a
// fresh activity row (recorded just now) within the interval window
// causes ShouldThrottle to return true so the caller skips emitting.
func TestShouldThrottle_WithinIntervalTrue(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	// Backdate the row 5 seconds ago — well within the 60 second interval.
	recent := time.Now().UTC().Add(-5 * time.Second).Format("2006-01-02T15:04:05")
	if _, err := db.Exec(
		"INSERT INTO activity (project_id, source, created_at) VALUES (?, ?, ?)",
		1, "claude", recent,
	); err != nil {
		t.Fatalf("seed activity: %v", err)
	}

	throttled, err := ShouldThrottle(1, "claude", 60)
	if err != nil {
		t.Fatalf("ShouldThrottle: %v", err)
	}
	if !throttled {
		t.Errorf("ShouldThrottle within 60s interval = false, want true")
	}
}

// TestShouldThrottle_OutsideIntervalFalse pins the out-of-interval
// branch: a row older than the interval lets the next event through.
// Backdate two hours ago against a 60-second interval.
func TestShouldThrottle_OutsideIntervalFalse(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	old := time.Now().UTC().Add(-2 * time.Hour).Format("2006-01-02T15:04:05")
	if _, err := db.Exec(
		"INSERT INTO activity (project_id, source, created_at) VALUES (?, ?, ?)",
		1, "claude", old,
	); err != nil {
		t.Fatalf("seed activity: %v", err)
	}

	throttled, err := ShouldThrottle(1, "claude", 60)
	if err != nil {
		t.Fatalf("ShouldThrottle: %v", err)
	}
	if throttled {
		t.Errorf("ShouldThrottle on 2h-old row with 60s interval = true, want false")
	}
}

// TestShouldThrottle_ScopedBySource confirms ShouldThrottle filters by
// (project_id, source): a recent row under a different source must not
// suppress the queried source.
func TestShouldThrottle_ScopedBySource(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	recent := time.Now().UTC().Add(-5 * time.Second).Format("2006-01-02T15:04:05")
	if _, err := db.Exec(
		"INSERT INTO activity (project_id, source, created_at) VALUES (?, ?, ?)",
		1, "other-source", recent,
	); err != nil {
		t.Fatalf("seed activity: %v", err)
	}

	throttled, err := ShouldThrottle(1, "claude", 60)
	if err != nil {
		t.Fatalf("ShouldThrottle: %v", err)
	}
	if throttled {
		t.Errorf("ShouldThrottle saw foreign source as same; got throttled=true")
	}
}
