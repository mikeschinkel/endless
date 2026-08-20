package monitor

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectPath_CanonicalPassesThrough pins the common branch: a path
// already stored in canonical form — which is what the Python CLI writes —
// comes back byte-identical, so normalizing on read costs nothing for a row
// registered normally.
func TestProjectPath_CanonicalPassesThrough(t *testing.T) {
	db := withTestDB(t)
	root := tempProjectRoot(t)
	seedProject(t, db, 1, "abs", root)

	got, err := ProjectPath(1)
	if err != nil {
		t.Fatalf("ProjectPath: %v", err)
	}
	if got != root {
		t.Errorf("ProjectPath = %q, want %q", got, root)
	}
}

// TestProjectPath_ResolvesSymlinkedRow pins the E-2002 read-side rule: a row
// holding an unresolved path (written before this normalization existed, or by
// hand) is returned resolved, so every path Go derives from it — worktree
// roots, lock files, the `/cd` line in a claim handoff — is the same string the
// Python CLI computes from the same row.
func TestProjectPath_ResolvesSymlinkedRow(t *testing.T) {
	db := withTestDB(t)
	real := tempProjectRoot(t)
	link := filepath.Join(t.TempDir(), "link-to-project")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	seedProject(t, db, 1, "linked", link)

	got, err := ProjectPath(1)
	if err != nil {
		t.Fatalf("ProjectPath: %v", err)
	}
	if got != real {
		t.Errorf("ProjectPath = %q, want %q (symlink resolved)", got, real)
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

// countProjects returns the number of rows in projects — the thing E-2002 is
// really about, since the symptom of a failed match is a SECOND project for a
// directory that already had one.
func countProjects(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM projects").Scan(&n); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	return n
}

// TestProjectIDForPath_SymlinkedCwdMatchesRegisteredRow is the E-2002
// regression. The Python CLI stores a symlink-resolved path; Claude Code hands
// the hook whatever spelling the user's shell had. Before the fix those two
// strings differed for any project reached through a symlink (every macOS
// project under /tmp or /var, and anyone whose projects live under a symlinked
// parent), the exact-match walk missed, and the hook auto-registered a second,
// empty project — leaving the session bound to it and SessionStart reporting
// "no tasks yet" for a project full of them.
func TestProjectIDForPath_SymlinkedCwdMatchesRegisteredRow(t *testing.T) {
	db := withTestDB(t)
	real := tempProjectRoot(t)
	seedProject(t, db, 1, "acme", real)

	link := filepath.Join(t.TempDir(), "link-to-acme")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	id, registered, err := ProjectIDForPath(link)
	if err != nil {
		t.Fatalf("ProjectIDForPath: %v", err)
	}
	if id != 1 || !registered {
		t.Errorf("(id, registered) = (%d, %v), want (1, true)", id, registered)
	}
	if n := countProjects(t, db); n != 1 {
		t.Errorf("projects rows = %d, want 1 (a symlinked cwd must not auto-register a duplicate)", n)
	}
}

// TestProjectIDForPath_SymlinkedSubdirWalksToRegisteredRoot covers the same
// mismatch one level down: the hook fires from a subdirectory reached through
// the symlink, so the walk-up has to run over normalized ancestors.
func TestProjectIDForPath_SymlinkedSubdirWalksToRegisteredRoot(t *testing.T) {
	db := withTestDB(t)
	real := tempProjectRoot(t)
	seedProject(t, db, 1, "acme", real)
	if err := os.MkdirAll(filepath.Join(real, "src", "pkg"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	link := filepath.Join(t.TempDir(), "link-to-acme")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	id, registered, err := ProjectIDForPath(filepath.Join(link, "src", "pkg"))
	if err != nil {
		t.Fatalf("ProjectIDForPath: %v", err)
	}
	if id != 1 || !registered {
		t.Errorf("(id, registered) = (%d, %v), want (1, true)", id, registered)
	}
	if n := countProjects(t, db); n != 1 {
		t.Errorf("projects rows = %d, want 1", n)
	}
}

// TestProjectIDForPath_UnresolvedRowStillMatches pins the other direction —
// the legacy-row tolerance that makes a data migration unnecessary. A row
// written before E-2002 holds an unresolved path; a canonical cwd must still
// find it rather than auto-register alongside it.
func TestProjectIDForPath_UnresolvedRowStillMatches(t *testing.T) {
	db := withTestDB(t)
	real := tempProjectRoot(t)
	link := filepath.Join(t.TempDir(), "link-to-acme")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	seedProject(t, db, 1, "acme", link) // stored unresolved, as a stale ledger holds it

	id, registered, err := ProjectIDForPath(real)
	if err != nil {
		t.Fatalf("ProjectIDForPath: %v", err)
	}
	if id != 1 || !registered {
		t.Errorf("(id, registered) = (%d, %v), want (1, true)", id, registered)
	}
	if n := countProjects(t, db); n != 1 {
		t.Errorf("projects rows = %d, want 1", n)
	}
}

// TestProjectIDForPath_UnresolvedRowsPickNearestProject pins the tie-break in
// the legacy scan: projects nest, and a cwd inside the inner one belongs to the
// inner one. The indexed walk gets that for free by visiting ancestors nearest
// -first; the scan has to choose the deepest match explicitly.
func TestProjectIDForPath_UnresolvedRowsPickNearestProject(t *testing.T) {
	db := withTestDB(t)
	outer := tempProjectRoot(t)
	inner := filepath.Join(outer, "nested")
	if err := os.MkdirAll(inner, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	linkDir := t.TempDir()
	outerLink := filepath.Join(linkDir, "outer")
	if err := os.Symlink(outer, outerLink); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	seedProject(t, db, 1, "outer", outerLink)
	seedProject(t, db, 2, "inner", filepath.Join(outerLink, "nested"))

	id, registered, err := ProjectIDForPath(inner)
	if err != nil {
		t.Fatalf("ProjectIDForPath: %v", err)
	}
	if id != 2 || !registered {
		t.Errorf("(id, registered) = (%d, %v), want (2, true) — nearest project wins", id, registered)
	}
}

// TestProjectIDForPath_DuplicateRowsPreferTheOlder pins what happens on a
// ledger that ALREADY caught the bug: the genuine registration and the
// auto-registered duplicate both denote the same directory. The lower id — the
// row that existed first, i.e. the real one with the tasks — is the answer.
func TestProjectIDForPath_DuplicateRowsPreferTheOlder(t *testing.T) {
	db := withTestDB(t)
	real := tempProjectRoot(t)
	link := filepath.Join(t.TempDir(), "link-to-acme")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	seedProject(t, db, 1, "acme", link)
	seedProject(t, db, 2, "acme-2", link+"/")

	id, registered, err := ProjectIDForPath(real)
	if err != nil {
		t.Fatalf("ProjectIDForPath: %v", err)
	}
	if id != 1 || !registered {
		t.Errorf("(id, registered) = (%d, %v), want (1, true)", id, registered)
	}
}

// TestNormalizeProjectPath_MissingLeafResolvesExistingPrefix pins the
// non-strict behavior that makes the Go half match pathlib.Path.resolve():
// filepath.EvalSymlinks fails outright when the leaf does not exist, which
// would leave a not-yet-created project directory unresolved and reintroduce
// the very mismatch this closes.
func TestNormalizeProjectPath_MissingLeafResolvesExistingPrefix(t *testing.T) {
	real := tempProjectRoot(t)
	link := filepath.Join(t.TempDir(), "link-to-parent")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got := NormalizeProjectPath(filepath.Join(link, "not", "created", "yet"))
	want := filepath.Join(real, "not", "created", "yet")
	if got != want {
		t.Errorf("NormalizeProjectPath = %q, want %q", got, want)
	}
}

// TestNormalizeProjectPath_ExpandsTilde pins ~ expansion, which ProjectPath
// used to do inline and now inherits from the shared rule — the Python half
// calls expanduser() before resolve() for the same reason.
func TestNormalizeProjectPath_ExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir resolvable")
	}
	got := NormalizeProjectPath("~/some-project")
	want := filepath.Join(NormalizeProjectPath(home), "some-project")
	if got != want {
		t.Errorf("NormalizeProjectPath(~/some-project) = %q, want %q", got, want)
	}
}

// ─── RepairProjectPaths (the E-2002 change script's data half) ──────────────

// repairInTx runs RepairProjectPaths against db inside its own transaction,
// the way the change script does.
func repairInTx(t *testing.T, db *sql.DB) ProjectPathRepair {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	repair, err := RepairProjectPaths(tx)
	if err != nil {
		tx.Rollback()
		t.Fatalf("RepairProjectPaths: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return repair
}

func projectPathOf(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	var path string
	if err := db.QueryRow("SELECT path FROM projects WHERE id = ?", id).Scan(&path); err != nil {
		t.Fatalf("read project %d: %v", id, err)
	}
	return path
}

// TestRepairProjectPaths_RewritesUnresolvedRow is the plain case: a ledger
// written before E-2002 holds the old spelling, and the repair replaces it with
// the canonical one so the indexed lookup — not the fallback scan — matches.
func TestRepairProjectPaths_RewritesUnresolvedRow(t *testing.T) {
	db := withTestDB(t)
	real := tempProjectRoot(t)
	link := filepath.Join(t.TempDir(), "link-to-acme")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	seedProject(t, db, 1, "acme", link)

	repair := repairInTx(t, db)

	if repair.Rewritten != 1 || repair.Merged != 0 {
		t.Errorf("repair = %+v, want {Merged:0 Rewritten:1}", repair)
	}
	if got := projectPathOf(t, db, 1); got != real {
		t.Errorf("path = %q, want %q", got, real)
	}
}

// TestRepairProjectPaths_MergesTheDuplicateIntoTheOlderRow is the case the
// bug actually produced: the genuine registration plus the auto-registered twin
// the hook created for the same directory. The older row survives with the
// canonical path and the twin is gone.
func TestRepairProjectPaths_MergesTheDuplicateIntoTheOlderRow(t *testing.T) {
	db := withTestDB(t)
	real := tempProjectRoot(t)
	link := filepath.Join(t.TempDir(), "link-to-acme")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	seedProject(t, db, 1, "acme", real)
	seedProject(t, db, 2, "acme-2", link)

	repair := repairInTx(t, db)

	if repair.Merged != 1 {
		t.Errorf("Merged = %d, want 1", repair.Merged)
	}
	if n := countProjects(t, db); n != 1 {
		t.Errorf("projects rows = %d, want 1", n)
	}
	if got := projectPathOf(t, db, 1); got != real {
		t.Errorf("surviving path = %q, want %q", got, real)
	}
}

// TestRepairProjectPaths_KeepsTheDuplicatesHistory is why the merge repoints
// instead of deleting. Every session that ran while the bug was live was bound
// to the auto-registered twin, and anything imported then hangs off it; a
// DELETE would cascade all of it away. The rows must land on the survivor.
func TestRepairProjectPaths_KeepsTheDuplicatesHistory(t *testing.T) {
	db := withTestDB(t)
	real := tempProjectRoot(t)
	link := filepath.Join(t.TempDir(), "link-to-acme")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	seedProject(t, db, 1, "acme", real)
	seedProject(t, db, 2, "acme-2", link)
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (55, 2, 'stranded', 'ready')",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, platform, state, started_at, last_activity)
		 VALUES (9, 'sess-dup', 2, 'claude', 'working', '2026-08-01T00:00:00', '2026-08-01T00:00:00')`,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	repairInTx(t, db)

	var taskProject, sessionProject int64
	if err := db.QueryRow("SELECT project_id FROM tasks WHERE id = 55").Scan(&taskProject); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if err := db.QueryRow("SELECT project_id FROM sessions WHERE id = 9").Scan(&sessionProject); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if taskProject != 1 {
		t.Errorf("task project_id = %d, want 1 (repointed, not cascaded away)", taskProject)
	}
	if sessionProject != 1 {
		t.Errorf("session project_id = %d, want 1", sessionProject)
	}
}

// TestRepairProjectPaths_SurvivesAUniqueRefCollision pins the one repointing
// that cannot be a plain UPDATE: project_next.project_id is UNIQUE, so a
// duplicate carrying a curated next list cannot move onto a survivor that
// already has one. The survivor keeps its list and the repair still completes.
func TestRepairProjectPaths_SurvivesAUniqueRefCollision(t *testing.T) {
	db := withTestDB(t)
	real := tempProjectRoot(t)
	link := filepath.Join(t.TempDir(), "link-to-acme")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	seedProject(t, db, 1, "acme", real)
	seedProject(t, db, 2, "acme-2", link)
	if _, err := db.Exec(
		"INSERT INTO project_next (id, project_id) VALUES (1, 1), (2, 2)",
	); err != nil {
		t.Fatalf("seed project_next: %v", err)
	}

	repairInTx(t, db)

	if n := countProjects(t, db); n != 1 {
		t.Errorf("projects rows = %d, want 1", n)
	}
	var nextRows, nextProject int64
	if err := db.QueryRow("SELECT count(*), min(project_id) FROM project_next").
		Scan(&nextRows, &nextProject); err != nil {
		t.Fatalf("read project_next: %v", err)
	}
	if nextRows != 1 || nextProject != 1 {
		t.Errorf("project_next = %d row(s) on project %d, want 1 on 1", nextRows, nextProject)
	}
}

// TestRepairProjectPaths_IsIdempotent pins the property that makes this safe to
// re-run: a second pass over an already-canonical ledger changes nothing.
func TestRepairProjectPaths_IsIdempotent(t *testing.T) {
	db := withTestDB(t)
	real := tempProjectRoot(t)
	link := filepath.Join(t.TempDir(), "link-to-acme")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	seedProject(t, db, 1, "acme", link)
	seedProject(t, db, 2, "other", tempProjectRoot(t))

	repairInTx(t, db)
	again := repairInTx(t, db)

	if again.Merged != 0 || again.Rewritten != 0 {
		t.Errorf("second pass = %+v, want a no-op", again)
	}
}

// TestRepairProjectPaths_LeavesUnrelatedProjectsAlone guards the blast radius:
// a project whose stored path is already canonical is not touched, and two
// genuinely different directories are never merged.
func TestRepairProjectPaths_LeavesUnrelatedProjectsAlone(t *testing.T) {
	db := withTestDB(t)
	first := tempProjectRoot(t)
	second := tempProjectRoot(t)
	seedProject(t, db, 1, "first", first)
	seedProject(t, db, 2, "second", second)

	repair := repairInTx(t, db)

	if repair.Merged != 0 || repair.Rewritten != 0 {
		t.Errorf("repair = %+v, want a no-op", repair)
	}
	if got := projectPathOf(t, db, 1); got != first {
		t.Errorf("first path = %q, want %q", got, first)
	}
	if got := projectPathOf(t, db, 2); got != second {
		t.Errorf("second path = %q, want %q", got, second)
	}
}

// TestProjectLookupNeverNormalizesWithAbsAlone is the guard against the exact
// revert that shipped the bug. `filepath.Abs` makes a path absolute and stops
// there, leaving symlink components in place; every project-path comparison in
// this package must go through NormalizeProjectPath instead.
//
// A source-level check because the failure is silent — a comparison that uses
// Abs alone works perfectly on any machine whose paths happen to have no
// symlinks, which is most development boxes and no macOS temp directory. It
// belongs here rather than in tests/tasks/e-2002-verify.sh, which is a pre-land
// gate for one task and stops protecting anything the moment that task lands.
func TestProjectLookupNeverNormalizesWithAbsAlone(t *testing.T) {
	for _, name := range []string{"db.go", "project_path.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if !strings.Contains(line, "filepath.Abs") {
				continue
			}
			// project_path.go's own use is the first step of the canonical
			// rule, not a shortcut past it.
			if name == "project_path.go" && strings.Contains(line, "abs, err := filepath.Abs(p)") {
				continue
			}
			t.Errorf("%s:%d normalizes a path with filepath.Abs alone: %s\n"+
				"use monitor.NormalizeProjectPath", name, i+1, strings.TrimSpace(line))
		}
	}
}
