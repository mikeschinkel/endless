package monitor

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProjectPath_AbsolutePassesThrough pins the no-expansion branch:
// a path stored as an absolute filesystem path is returned verbatim
// without ~ expansion applied.
func TestProjectPath_AbsolutePassesThrough(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "abs", "/tmp/abs-proj")

	got, err := ProjectPath(1)
	if err != nil {
		t.Fatalf("ProjectPath: %v", err)
	}
	if got != "/tmp/abs-proj" {
		t.Errorf("ProjectPath = %q, want /tmp/abs-proj", got)
	}
}

// TestProjectPath_TildeExpandsToHome pins the ~ expansion branch:
// a path stored as "~/foo" is rewritten to $HOME/foo so callers can
// use the returned value as a real filesystem path.
func TestProjectPath_TildeExpandsToHome(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "tilde", "~/some-project")

	got, err := ProjectPath(1)
	if err != nil {
		t.Fatalf("ProjectPath: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	want := filepath.Join(home, "some-project")
	if got != want {
		t.Errorf("ProjectPath = %q, want %q", got, want)
	}
}

// TestProjectPath_MissingRowReturnsError confirms an unknown id
// surfaces sql.ErrNoRows so callers can distinguish "no project" from
// "registered with empty path".
func TestProjectPath_MissingRowReturnsError(t *testing.T) {
	withTestDB(t)
	if _, err := ProjectPath(999999); err == nil {
		t.Errorf("ProjectPath on missing row = nil error, want sql.ErrNoRows")
	}
}

// TestProjectIDForPath_ExactMatch pins the happy path: a directory
// that exactly matches a registered project's path returns (id, true,
// nil) — the second return value signals "registered" (vs auto-created).
func TestProjectIDForPath_ExactMatch(t *testing.T) {
	db := withTestDB(t)
	dir := t.TempDir()
	seedProject(t, db, 1, "exact", dir)

	id, registered, err := ProjectIDForPath(dir)
	if err != nil {
		t.Fatalf("ProjectIDForPath: %v", err)
	}
	if id != 1 {
		t.Errorf("id = %d, want 1", id)
	}
	if !registered {
		t.Errorf("registered = false, want true (exact match should be 'found')")
	}
}

// TestProjectIDForPath_ParentWalkMatch pins the upward walk: a working
// directory inside a registered project root resolves to that root's
// id. This is how the hook attributes events fired from a subdirectory.
func TestProjectIDForPath_ParentWalkMatch(t *testing.T) {
	db := withTestDB(t)
	root := t.TempDir()
	seedProject(t, db, 1, "walk", root)
	child := filepath.Join(root, "subdir", "deeper")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	id, registered, err := ProjectIDForPath(child)
	if err != nil {
		t.Fatalf("ProjectIDForPath: %v", err)
	}
	if id != 1 {
		t.Errorf("id = %d, want 1 (walk-up should resolve to root)", id)
	}
	if !registered {
		t.Errorf("registered = false, want true (parent-match counts as registered)")
	}
}

// TestProjectIDForPath_UnknownAutoRegisters pins the fallback branch:
// when no exact or ancestor match exists, ensureAutoRegisteredProject
// inserts a row (status='active') and returns (newID, false). The
// false signals to the caller that this was an auto-registration —
// useful for emitting a one-time "we registered you" notice.
func TestProjectIDForPath_UnknownAutoRegisters(t *testing.T) {
	db := withTestDB(t)
	dir := t.TempDir()

	id, registered, err := ProjectIDForPath(dir)
	if err != nil {
		t.Fatalf("ProjectIDForPath: %v", err)
	}
	if id == 0 {
		t.Errorf("id = 0, want a fresh inserted row id")
	}
	if registered {
		t.Errorf("registered = true, want false (auto-register should report 'not previously registered')")
	}
	var status string
	if err := db.QueryRow(
		"SELECT status FROM projects WHERE id=?", id,
	).Scan(&status); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != "active" {
		t.Errorf("auto-registered status = %q, want active", status)
	}
}
