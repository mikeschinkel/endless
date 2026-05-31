package monitor

import (
	"testing"
)

// TestRecordSuggestion_InsertsOpenRow verifies that RecordSuggestion writes an
// open suggestion (task_id NULL) with the supplied source / trigger / body and
// the resulting row is visible via the read APIs.
func TestRecordSuggestion_InsertsOpenRow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := RecordSuggestion("sess-A", 1, "enforcement", "ctx-x", "tighten X"); err != nil {
		t.Fatalf("RecordSuggestion: %v", err)
	}

	open, err := ListOpenSuggestions(1)
	if err != nil {
		t.Fatalf("ListOpenSuggestions: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("got %d open suggestions, want 1", len(open))
	}
	s := open[0]
	if s.SessionID != "sess-A" {
		t.Errorf("SessionID = %q, want sess-A", s.SessionID)
	}
	if s.Source != "enforcement" {
		t.Errorf("Source = %q, want enforcement", s.Source)
	}
	if s.TriggerCtx != "ctx-x" {
		t.Errorf("TriggerCtx = %q, want ctx-x", s.TriggerCtx)
	}
	if s.Suggestion != "tighten X" {
		t.Errorf("Suggestion = %q, want 'tighten X'", s.Suggestion)
	}
	if s.TaskID != nil {
		t.Errorf("TaskID = %v, want nil (open suggestion)", *s.TaskID)
	}
	if s.ProjectID == nil || *s.ProjectID != 1 {
		t.Errorf("ProjectID = %v, want *1", s.ProjectID)
	}
}

// TestRecordSuggestion_ZeroProjectIsNull verifies the projectID=0 sentinel
// stores NULL so the row is visible to cross-project listing (ListOpenSuggestions(0))
// but not attributed to any project.
func TestRecordSuggestion_ZeroProjectIsNull(t *testing.T) {
	withTestDB(t)
	if err := RecordSuggestion("sess-A", 0, "auto", "", "no project"); err != nil {
		t.Fatalf("RecordSuggestion: %v", err)
	}
	all, err := ListOpenSuggestions(0)
	if err != nil {
		t.Fatalf("ListOpenSuggestions(0): %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d, want 1", len(all))
	}
	if all[0].ProjectID != nil {
		t.Errorf("ProjectID = %v, want nil (project_id NULL)", *all[0].ProjectID)
	}
}

// TestListOpenSuggestions_ExcludesAccepted verifies that AcceptSuggestion takes
// rows off the open list — a sanity check that "open" is defined by task_id IS
// NULL across the read API.
func TestListOpenSuggestions_ExcludesAccepted(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := RecordSuggestion("sess-A", 1, "src", "", "keep open"); err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	if err := RecordSuggestion("sess-A", 1, "src", "", "to be accepted"); err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	// Seed a task to link via AcceptSuggestion.
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		100, 1, "host", "ready",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	open, err := ListOpenSuggestions(1)
	if err != nil {
		t.Fatalf("ListOpenSuggestions before accept: %v", err)
	}
	if len(open) != 2 {
		t.Fatalf("got %d open before accept, want 2", len(open))
	}

	// Accept one — must drop from open list.
	target := open[0].ID
	if err := AcceptSuggestion(target, 100); err != nil {
		t.Fatalf("AcceptSuggestion: %v", err)
	}
	open, err = ListOpenSuggestions(1)
	if err != nil {
		t.Fatalf("ListOpenSuggestions after accept: %v", err)
	}
	if len(open) != 1 {
		t.Errorf("got %d open after accept, want 1", len(open))
	}
}

// TestListOpenSuggestions_ScopesToProject verifies the project=N filter excludes
// suggestions from other projects (open-only path).
func TestListOpenSuggestions_ScopesToProject(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedProject(t, db, 2, "proj-test-2", "/tmp/proj-test-2")

	if err := RecordSuggestion("sess-A", 1, "src", "", "p1-a"); err != nil {
		t.Fatalf("seed p1: %v", err)
	}
	if err := RecordSuggestion("sess-A", 2, "src", "", "p2-a"); err != nil {
		t.Fatalf("seed p2: %v", err)
	}

	p1, err := ListOpenSuggestions(1)
	if err != nil {
		t.Fatalf("p1: %v", err)
	}
	if len(p1) != 1 || p1[0].Suggestion != "p1-a" {
		t.Errorf("project 1 got %+v, want single p1-a", p1)
	}
	_ = db
}

// TestListAllSuggestions_IncludesAccepted verifies that, unlike the open list,
// ListAllSuggestions returns both open and accepted rows for the project.
func TestListAllSuggestions_IncludesAccepted(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		77, 1, "host", "ready",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := RecordSuggestion("sess-A", 1, "src", "", "open one"); err != nil {
		t.Fatalf("seed open: %v", err)
	}
	if err := RecordSuggestion("sess-A", 1, "src", "", "accepted one"); err != nil {
		t.Fatalf("seed accepted: %v", err)
	}
	open, err := ListOpenSuggestions(1)
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	// Accept whichever the "accepted one" row is.
	for _, s := range open {
		if s.Suggestion == "accepted one" {
			if err := AcceptSuggestion(s.ID, 77); err != nil {
				t.Fatalf("accept: %v", err)
			}
			break
		}
	}

	all, err := ListAllSuggestions(1)
	if err != nil {
		t.Fatalf("ListAllSuggestions: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("got %d rows, want 2 (open + accepted)", len(all))
	}
}

// TestGetSuggestion_ReturnsRow verifies a single-row lookup hands back the row
// with the expected body.
func TestGetSuggestion_ReturnsRow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if err := RecordSuggestion("sess-A", 1, "src", "ctx", "body"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	open, err := ListOpenSuggestions(1)
	if err != nil || len(open) != 1 {
		t.Fatalf("setup got len=%d err=%v", len(open), err)
	}
	got, err := GetSuggestion(open[0].ID)
	if err != nil {
		t.Fatalf("GetSuggestion: %v", err)
	}
	if got.Suggestion != "body" {
		t.Errorf("Suggestion = %q, want body", got.Suggestion)
	}
	_ = db
}

// TestGetSuggestion_NotFoundErrors verifies the explicit not-found path —
// callers branch on the error message to distinguish from real DB failures.
func TestGetSuggestion_NotFoundErrors(t *testing.T) {
	withTestDB(t)
	if _, err := GetSuggestion(999999); err == nil {
		t.Fatal("GetSuggestion on missing id returned nil, want error")
	}
}

// TestAcceptSuggestion_SetsTaskID verifies that on accept, task_id is populated
// and a re-accept attempt fails (the WHERE clause requires task_id IS NULL).
func TestAcceptSuggestion_SetsTaskID(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		55, 1, "host", "ready",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := RecordSuggestion("sess-A", 1, "src", "", "body"); err != nil {
		t.Fatalf("seed suggestion: %v", err)
	}
	open, _ := ListOpenSuggestions(1)
	if len(open) != 1 {
		t.Fatalf("got %d open, want 1", len(open))
	}
	id := open[0].ID

	if err := AcceptSuggestion(id, 55); err != nil {
		t.Fatalf("AcceptSuggestion: %v", err)
	}
	got, err := GetSuggestion(id)
	if err != nil {
		t.Fatalf("GetSuggestion: %v", err)
	}
	if got.TaskID == nil || *got.TaskID != 55 {
		t.Errorf("TaskID = %v, want *55", got.TaskID)
	}
	// Re-accept must fail (already accepted).
	if err := AcceptSuggestion(id, 55); err == nil {
		t.Error("second AcceptSuggestion returned nil, want already-accepted error")
	}
}

// TestCountOpenSuggestions_ReflectsAccepts verifies the count drops as rows are
// accepted into tasks (open == task_id IS NULL).
func TestCountOpenSuggestions_ReflectsAccepts(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		88, 1, "host", "ready",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := RecordSuggestion("sess-A", 1, "src", "", "body"); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	n, err := CountOpenSuggestions(1)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3", n)
	}

	open, _ := ListOpenSuggestions(1)
	if err := AcceptSuggestion(open[0].ID, 88); err != nil {
		t.Fatalf("accept: %v", err)
	}
	n, err = CountOpenSuggestions(1)
	if err != nil {
		t.Fatalf("count 2: %v", err)
	}
	if n != 2 {
		t.Errorf("count after one accept = %d, want 2", n)
	}
}
