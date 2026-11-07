package monitor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// TestClearIgnoredDirectory_RefusesAnIgnoredRowWithSessionsButNoTasks is the
// E-2010 bug. The guard counted `tasks` alone, so a former project whose tasks
// had been moved or removed but whose sessions, activity and notes were still
// recorded against it passed the check — and eight of the ten references to
// projects are ON DELETE CASCADE, so clearing it took all of them.
// Sessions in particular carry ES-NNNN ids that session_tasks, session_notices
// and activity refer to, which is exactly what ED-1547 forbids re-minting.
func TestClearIgnoredDirectory_RefusesAnIgnoredRowWithSessionsButNoTasks(t *testing.T) {
	db := withTestDB(t)
	root := tempProjectRoot(t)
	seedIgnored(t, root)

	var id int64
	if err := db.QueryRow("SELECT id FROM projects WHERE path = ?", root).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, started_at)
		 VALUES (9001, 'sess-e2010', ?, 'ended', '2026-11-07T10:00:00')`, id,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	_, err := ClearIgnoredDirectory(db, root)
	if !errors.Is(err, ErrIgnoredHasHistory) {
		t.Fatalf("err = %v, want ErrIgnoredHasHistory — a project with sessions "+
			"but no tasks was deleted, cascading its history away", err)
	}
	if !strings.Contains(err.Error(), "1 session") {
		t.Errorf("refusal = %q, want it to name the session it would have taken", err)
	}
	if n := countProjects(t, db); n != 1 {
		t.Errorf("projects rows = %d, want 1 — the refusal must change nothing", n)
	}
	var sessions int
	if err := db.QueryRow("SELECT count(*) FROM sessions WHERE id = 9001").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 {
		t.Errorf("sessions rows = %d, want 1", sessions)
	}
}

// TestClearIgnoredDirectory_NamesEverythingItWouldHaveTaken pins that the
// refusal lists every kind of row, not just the first one found. Someone who
// meant to clear the row needs to know what is on it to decide what to do
// instead, and one name at a time turns that into a guessing game.
func TestClearIgnoredDirectory_NamesEverythingItWouldHaveTaken(t *testing.T) {
	db := withTestDB(t)
	root := tempProjectRoot(t)
	seedIgnored(t, root)

	var id int64
	if err := db.QueryRow("SELECT id FROM projects WHERE path = ?", root).Scan(&id); err != nil {
		t.Fatal(err)
	}
	seedProjectStatusTask(t, db, 7701, id, "ready")
	seedProjectStatusTask(t, db, 7702, id, "ready")
	if _, err := db.Exec(
		`INSERT INTO notes (project_id, note_type, message, created_at)
		 VALUES (?, 'staleness', 'a note', '2026-11-07T10:00:00')`, id,
	); err != nil {
		t.Fatalf("seed note: %v", err)
	}

	_, err := ClearIgnoredDirectory(db, root)
	if !errors.Is(err, ErrIgnoredHasHistory) {
		t.Fatalf("err = %v, want ErrIgnoredHasHistory", err)
	}
	for _, want := range []string{"2 tasks", "1 note"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal = %q, want it to name %q", err, want)
		}
	}
}

// TestProjectReferences_CoversEverySchemaReference reads the foreign keys out
// of the live schema and fails when projectReferences disagrees with them.
//
// The guard list is the only thing standing between `project unignore` and a
// cascading delete, and a table added to schema.sql later will not announce
// itself. Without this test the list silently rots and the hole E-2010 closed
// reopens one migration at a time.
func TestProjectReferences_CoversEverySchemaReference(t *testing.T) {
	db := withTestDB(t)

	rows, err := db.Query(`
		SELECT m.name, fk."from"
		  FROM sqlite_master AS m
		  JOIN pragma_foreign_key_list(m.name) AS fk
		 WHERE m.type = 'table' AND fk."table" = 'projects'`)
	if err != nil {
		t.Fatalf("reading foreign keys: %v", err)
	}
	defer rows.Close()

	declared := map[string]bool{}
	for _, ref := range projectReferences {
		declared[ref.table+"."+ref.column] = true
	}

	found := map[string]bool{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		key := table + "." + column
		found[key] = true
		if !declared[key] {
			t.Errorf("%s references projects(id) and is missing from "+
				"projectReferences, so `project unignore` would delete a project "+
				"holding those rows — add it there", key)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("no foreign keys to projects found — the query is wrong, not the schema")
	}
	for key := range declared {
		if !found[key] {
			t.Errorf("projectReferences lists %s, which no longer references "+
				"projects(id) — remove it", key)
		}
	}
}
