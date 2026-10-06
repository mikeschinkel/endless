package monitor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// seedIgnored writes an ignored row the way SetDirectoryIgnored does.
func seedIgnored(t *testing.T, dir string) {
	t.Helper()
	db, err := DB()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := SetDirectoryIgnored(db, dir); err != nil {
		t.Fatalf("SetDirectoryIgnored(%s): %v", dir, err)
	}
}

func mkdirs(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestProjectIDForPath_IgnoredSubtreeIsNeverAutoRegistered is the E-2251 bug:
// a directory under an ignored one (go-3rd-party/h2) was auto-registered by the
// hook because only `project discover` read the ignore list.
func TestProjectIDForPath_IgnoredSubtreeIsNeverAutoRegistered(t *testing.T) {
	db := withTestDB(t)
	root := tempProjectRoot(t)
	seedIgnored(t, root)
	child := mkdirs(t, root, "vendor-repo", "sub")
	before := countProjects(t, db)

	_, _, err := ProjectIDForPath(child)
	if !errors.Is(err, ErrIgnoredDirectory) {
		t.Fatalf("err = %v, want ErrIgnoredDirectory", err)
	}
	if got := countProjects(t, db); got != before {
		t.Errorf("projects rows = %d, want %d (nothing auto-registered)", got, before)
	}
}

// TestProjectIDForPath_NearestRowWins pins Mike's ~/Projects example: the
// parent is ignored, a project inside it is registered, and a cwd inside that
// project resolves to it.
func TestProjectIDForPath_NearestRowWins(t *testing.T) {
	db := withTestDB(t)
	root := tempProjectRoot(t)
	seedIgnored(t, root)
	proj := mkdirs(t, root, "endless")
	seedProject(t, db, 50, "endless", proj)
	cwd := mkdirs(t, proj, "internal")

	id, registered, err := ProjectIDForPath(cwd)
	if err != nil {
		t.Fatalf("ProjectIDForPath: %v", err)
	}
	if id != 50 || !registered {
		t.Errorf("got (%d, %v), want (50, true)", id, registered)
	}

	// And an ignored row inside a project carves its subtree back out.
	vendored := mkdirs(t, proj, "third_party")
	seedIgnored(t, vendored)
	if _, _, err := ProjectIDForPath(mkdirs(t, vendored, "x")); !errors.Is(err, ErrIgnoredDirectory) {
		t.Errorf("under an ignored dir inside a project: err = %v, want ErrIgnoredDirectory", err)
	}
}

// TestResolveDirectory_MarkerFileIgnores pins the on-disk marker: a directory
// holding .endless-ignore is ignored with no row at all, and the marker beats a
// row at the same rung.
func TestResolveDirectory_MarkerFileIgnores(t *testing.T) {
	db := withTestDB(t)
	root := tempProjectRoot(t)
	if err := os.WriteFile(filepath.Join(root, IgnoreMarker), []byte("vendored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	child := mkdirs(t, root, "a")

	v, err := ResolveDirectory(db, child)
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != VerdictIgnored || !v.Marker {
		t.Errorf("verdict = %+v, want ignored by marker", v)
	}

	seedProject(t, db, 7, "also-registered", root)
	v, err = ResolveDirectory(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != VerdictIgnored {
		t.Errorf("marker and active row at one rung: kind = %q, want ignored", v.Kind)
	}
}

// TestResolveDirectory_None reports a directory nothing covers.
func TestResolveDirectory_None(t *testing.T) {
	db := withTestDB(t)
	v, err := ResolveDirectory(db, tempProjectRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != VerdictNone {
		t.Errorf("kind = %q, want none", v.Kind)
	}
}

// TestSetDirectoryIgnored_KeepsAFormerProjectsRow pins unregister: the row and
// its name survive, only the status changes — so its tasks keep their project.
func TestSetDirectoryIgnored_KeepsAFormerProjectsRow(t *testing.T) {
	db := withTestDB(t)
	root := tempProjectRoot(t)
	seedProject(t, db, 9, "former", root)

	_, existed, err := SetDirectoryIgnored(db, root)
	if err != nil || !existed {
		t.Fatalf("SetDirectoryIgnored = (%v, %v), want existed", existed, err)
	}
	var name, status string
	if err := db.QueryRow("SELECT name, status FROM projects WHERE id = 9").Scan(&name, &status); err != nil {
		t.Fatal(err)
	}
	if name != "former" || status != ProjectStatusIgnored {
		t.Errorf("row = (%q, %q), want (former, ignored)", name, status)
	}
	var live int
	if err := db.QueryRow("SELECT count(*) FROM live_projects WHERE id = 9").Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Errorf("live_projects still lists the ignored row")
	}

	if _, err := SetDirectoryActive(db, root); err != nil {
		t.Fatalf("SetDirectoryActive: %v", err)
	}
	if _, _, err := ProjectIDForPath(root); err != nil {
		t.Errorf("re-activated directory: %v", err)
	}
}

// TestSetDirectoryIgnored_NamesANewRowByPath pins the naming rule for a
// directory that never was a project: its stored path, which cannot collide
// with a basename-derived project name.
func TestSetDirectoryIgnored_NamesANewRowByPath(t *testing.T) {
	db := withTestDB(t)
	root := tempProjectRoot(t)
	stored, existed, err := SetDirectoryIgnored(db, root)
	if err != nil || existed {
		t.Fatalf("SetDirectoryIgnored = (%v, %v)", existed, err)
	}
	var name string
	if err := db.QueryRow("SELECT name FROM projects WHERE path = ?", stored).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != stored {
		t.Errorf("name = %q, want the stored path %q", name, stored)
	}
	if _, err := ClearIgnoredDirectory(db, root); err != nil {
		t.Fatalf("ClearIgnoredDirectory: %v", err)
	}
	if n := countProjects(t, db); n != 0 {
		t.Errorf("rows after clear = %d, want 0", n)
	}
}

// TestClearIgnoredDirectory_RefusesARegisteredProject keeps `unignore` from
// deleting a live project's row.
func TestClearIgnoredDirectory_RefusesARegisteredProject(t *testing.T) {
	db := withTestDB(t)
	root := tempProjectRoot(t)
	seedProject(t, db, 3, "live", root)
	if _, err := ClearIgnoredDirectory(db, root); !errors.Is(err, ErrNotIgnored) {
		t.Errorf("err = %v, want ErrNotIgnored", err)
	}
}
