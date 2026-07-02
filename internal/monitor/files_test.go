package monitor

import (
	"testing"
)

// TestGetTaskTitle_ReturnsTitle pins the happy path: a tasks row's title is
// returned verbatim. GetTaskTitle is the read-side used by the status renderer
// and the Layer 1 reminder; both rely on the literal column value.
func TestGetTaskTitle_ReturnsTitle(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	want := "Implement the widget"
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		77, 1, want, "ready",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	got, err := GetTaskTitle(77)
	if err != nil {
		t.Fatalf("GetTaskTitle: %v", err)
	}
	if got != want {
		t.Errorf("GetTaskTitle = %q, want %q", got, want)
	}
}

// TestGetTaskTitle_MissingRowReturnsEmpty pins the sql.ErrNoRows branch:
// an unknown task id returns "", nil so callers can render an empty
// placeholder without special-casing absence.
func TestGetTaskTitle_MissingRowReturnsEmpty(t *testing.T) {
	withTestDB(t)
	got, err := GetTaskTitle(424242)
	if err != nil {
		t.Fatalf("GetTaskTitle: %v", err)
	}
	if got != "" {
		t.Errorf("GetTaskTitle on missing row = %q, want \"\"", got)
	}
}
