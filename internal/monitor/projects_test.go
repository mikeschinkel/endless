package monitor

import (
	"database/sql"
	"errors"
	"io/fs"
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

// ─── the two forms (E-2011) ─────────────────────────────────────────────────

// withTempHome points $HOME at a fresh directory and returns it in resolved
// form. Every home-relative assertion needs a home it controls: asserting
// against the developer's real $HOME would pass on this machine and prove
// nothing about the rule.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	resolved, err := ResolvedProjectPath(home)
	if err != nil {
		t.Fatalf("ResolvedProjectPath(home): %v", err)
	}
	return resolved
}

// TestResolvedProjectPath_MissingLeafResolvesExistingPrefix pins the
// non-strict behavior that makes the Go half match pathlib.Path.resolve():
// filepath.EvalSymlinks fails outright when the leaf does not exist, which
// would leave a not-yet-created project directory unresolved and reintroduce
// the very mismatch this closes.
func TestResolvedProjectPath_MissingLeafResolvesExistingPrefix(t *testing.T) {
	real := tempProjectRoot(t)
	link := filepath.Join(t.TempDir(), "link-to-parent")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := ResolvedProjectPath(filepath.Join(link, "not", "created", "yet"))
	if err != nil {
		t.Fatalf("ResolvedProjectPath: %v", err)
	}
	want := filepath.Join(real, "not", "created", "yet")
	if got != want {
		t.Errorf("ResolvedProjectPath = %q, want %q", got, want)
	}
}

// TestResolvedProjectPath_ExpandsTilde pins the read half of the storage rule.
// Since E-2011 the tilde is the shape projects.path normally holds, so every
// caller that treats the result as a directory depends on this branch — and
// bare filepath.Abs on `~/x` yields `<cwd>/~/x`, which fails much later and
// somewhere else.
func TestResolvedProjectPath_ExpandsTilde(t *testing.T) {
	home := withTempHome(t)

	got, err := ResolvedProjectPath("~/some-project")
	if err != nil {
		t.Fatalf("ResolvedProjectPath: %v", err)
	}
	want := filepath.Join(home, "some-project")
	if got != want {
		t.Errorf("ResolvedProjectPath(~/some-project) = %q, want %q", got, want)
	}

	got, err = ResolvedProjectPath("~")
	if err != nil {
		t.Fatalf("ResolvedProjectPath(~): %v", err)
	}
	if got != home {
		t.Errorf("ResolvedProjectPath(~) = %q, want %q", got, home)
	}
}

// TestStoredProjectPath_IsHomeRelative is the rule itself: what the column
// holds for a project under $HOME, and the reason `endless sql` output is
// readable.
func TestStoredProjectPath_IsHomeRelative(t *testing.T) {
	home := withTempHome(t)

	got, err := StoredProjectPath(filepath.Join(home, "Projects", "acme"))
	if err != nil {
		t.Fatalf("StoredProjectPath: %v", err)
	}
	if got != "~/Projects/acme" {
		t.Errorf("StoredProjectPath = %q, want %q", got, "~/Projects/acme")
	}
}

// TestStoredProjectPath_HomeItselfIsBareTilde covers the boundary case the
// prefix test cannot: $HOME is under $HOME, and `~/` + "" would be `~/`.
func TestStoredProjectPath_HomeItselfIsBareTilde(t *testing.T) {
	home := withTempHome(t)

	got, err := StoredProjectPath(home)
	if err != nil {
		t.Fatalf("StoredProjectPath: %v", err)
	}
	if got != "~" {
		t.Errorf("StoredProjectPath(home) = %q, want %q", got, "~")
	}
}

// TestStoredProjectPath_OutsideHomeStaysAbsolute pins the mixed column ED-1562
// chose deliberately: /opt/src/acme has no home-relative spelling, so it keeps
// the absolute one rather than acquiring a `../..` that no reader could parse.
func TestStoredProjectPath_OutsideHomeStaysAbsolute(t *testing.T) {
	withTempHome(t)
	outside := tempProjectRoot(t) // a temp dir, not under the temp home

	got, err := StoredProjectPath(outside)
	if err != nil {
		t.Fatalf("StoredProjectPath: %v", err)
	}
	if got != outside {
		t.Errorf("StoredProjectPath = %q, want %q (unchanged)", got, outside)
	}
}

// TestStoredProjectPath_DoesNotSwallowASiblingOfHome guards the off-by-one that
// a naive prefix test has: /Users/mikey does not live inside /Users/mike, and
// storing it as `~y` would point the expansion at a directory that never
// existed.
func TestStoredProjectPath_DoesNotSwallowASiblingOfHome(t *testing.T) {
	home := withTempHome(t)
	sibling := home + "-sibling"
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := StoredProjectPath(sibling)
	if err != nil {
		t.Fatalf("StoredProjectPath: %v", err)
	}
	if got != sibling {
		t.Errorf("StoredProjectPath = %q, want %q (a sibling of home is not inside it)", got, sibling)
	}
}

// TestStoredProjectPath_ResolvesSymlinksBeforeRelativizing pins that E-2011
// re-points E-2002's invariant rather than weakening it: the stored spelling
// still must not depend on how the caller's shell spelled the path.
func TestStoredProjectPath_ResolvesSymlinksBeforeRelativizing(t *testing.T) {
	home := withTempHome(t)
	real := filepath.Join(home, "Projects", "acme")
	if err := os.MkdirAll(real, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(t.TempDir(), "link-to-acme")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := StoredProjectPath(link)
	if err != nil {
		t.Fatalf("StoredProjectPath: %v", err)
	}
	if got != "~/Projects/acme" {
		t.Errorf("StoredProjectPath(symlink) = %q, want %q", got, "~/Projects/acme")
	}
}

// TestStoredProjectPath_RoundTripsThroughResolved is the property the whole
// split rests on: the two accessors are inverses for any path, so nothing is
// lost by storing the shorter form.
func TestStoredProjectPath_RoundTripsThroughResolved(t *testing.T) {
	home := withTempHome(t)
	for _, dir := range []string{
		home,
		filepath.Join(home, "Projects", "acme"),
		tempProjectRoot(t),
	} {
		stored, err := StoredProjectPath(dir)
		if err != nil {
			t.Fatalf("StoredProjectPath(%s): %v", dir, err)
		}
		back, err := ResolvedProjectPath(stored)
		if err != nil {
			t.Fatalf("ResolvedProjectPath(%s): %v", stored, err)
		}
		if back != dir {
			t.Errorf("round trip of %q via %q = %q", dir, stored, back)
		}
	}
}

// TestProjectPathsFailLoudlyWithoutHome pins the contract E-2011 asked for by
// name. A silent answer here is `<cwd>/~/Projects/acme` on the read side and a
// second spelling of an already-registered directory on the write side; both
// surface far from the cause, so both refuse instead.
//
// A path with no tilde needs no home and so must still succeed — that is what
// keeps a machine with a broken $HOME able to look up a project stored
// absolutely.
func TestProjectPathsFailLoudlyWithoutHome(t *testing.T) {
	outside := tempProjectRoot(t)
	t.Setenv("HOME", "")

	if _, err := ResolvedProjectPath("~/Projects/acme"); !errors.Is(err, ErrNoHomeDir) {
		t.Errorf("ResolvedProjectPath(~/…) error = %v, want ErrNoHomeDir", err)
	}
	if _, err := StoredProjectPath(outside); !errors.Is(err, ErrNoHomeDir) {
		t.Errorf("StoredProjectPath error = %v, want ErrNoHomeDir", err)
	}
	got, err := ResolvedProjectPath(outside)
	if err != nil {
		t.Errorf("ResolvedProjectPath(absolute) = %v, want no error", err)
	}
	if got != outside {
		t.Errorf("ResolvedProjectPath(absolute) = %q, want %q", got, outside)
	}
}

// ─── the two forms, through the DB (E-2011) ─────────────────────────────────

// TestProjectIDForPath_MatchesAHomeRelativeRow is the shape every row takes
// after E-2011: the column holds `~/…` and the hook hands in an absolute cwd.
// If the walk did not convert each rung to the stored form, this would miss and
// auto-register a duplicate — the E-2002 failure, in a new spelling.
func TestProjectIDForPath_MatchesAHomeRelativeRow(t *testing.T) {
	db := withTestDB(t)
	home := withTempHome(t)
	root := filepath.Join(home, "Projects", "acme")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seedProject(t, db, 1, "acme", "~/Projects/acme")

	for _, dir := range []string{root, filepath.Join(root, "src")} {
		id, registered, err := ProjectIDForPath(dir)
		if err != nil {
			t.Fatalf("ProjectIDForPath(%s): %v", dir, err)
		}
		if id != 1 || !registered {
			t.Errorf("ProjectIDForPath(%s) = (%d, %v), want (1, true)", dir, id, registered)
		}
	}
	if n := countProjects(t, db); n != 1 {
		t.Errorf("projects rows = %d, want 1", n)
	}
}

// TestProjectIDForPath_AutoRegistersInStoredForm pins the write side of the
// same loop. The hook auto-registers whatever it could not find; writing that
// row absolute would leave the column mixed for no reason and make the NEXT
// lookup take the fallback scan instead of the index.
func TestProjectIDForPath_AutoRegistersInStoredForm(t *testing.T) {
	db := withTestDB(t)
	home := withTempHome(t)
	root := filepath.Join(home, "Projects", "newcomer")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	id, registered, err := ProjectIDForPath(root)
	if err != nil {
		t.Fatalf("ProjectIDForPath: %v", err)
	}
	if registered {
		t.Errorf("registered = true, want false (this is an auto-registration)")
	}
	if got := projectPathOf(t, db, id); got != "~/Projects/newcomer" {
		t.Errorf("auto-registered path = %q, want %q", got, "~/Projects/newcomer")
	}
	if n := countProjects(t, db); n != 1 {
		t.Errorf("projects rows = %d, want 1", n)
	}
}

// TestProjectPath_HomeRelativeRowComesBackResolved is the read half at the DB
// boundary: every caller of ProjectPath uses the answer as a directory, so the
// stored tilde must never escape this function.
func TestProjectPath_HomeRelativeRowComesBackResolved(t *testing.T) {
	db := withTestDB(t)
	home := withTempHome(t)
	seedProject(t, db, 1, "acme", "~/Projects/acme")

	got, err := ProjectPath(1)
	if err != nil {
		t.Fatalf("ProjectPath: %v", err)
	}
	want := filepath.Join(home, "Projects", "acme")
	if got != want {
		t.Errorf("ProjectPath = %q, want %q", got, want)
	}
}

// TestMatchProjectPath_FindsBothSpellings pins the fast path and the fallback
// side by side: a row written the E-2011 way is matched on the indexed exact
// compare, and a row still written the E-2002 way (absolute) is matched by the
// resolved-form scan — so upgrading a ledger cannot spawn duplicates before the
// change script runs.
func TestMatchProjectPath_FindsBothSpellings(t *testing.T) {
	db := withTestDB(t)
	home := withTempHome(t)
	root := filepath.Join(home, "Projects", "acme")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	seedProject(t, db, 1, "acme", "~/Projects/acme")
	stored, found, err := MatchProjectPath(db, root)
	if err != nil || !found {
		t.Fatalf("MatchProjectPath = (%q, %v, %v)", stored, found, err)
	}
	if stored != "~/Projects/acme" {
		t.Errorf("stored = %q, want %q", stored, "~/Projects/acme")
	}

	if _, err = db.Exec("UPDATE projects SET path = ? WHERE id = 1", root); err != nil {
		t.Fatalf("rewrite row absolute: %v", err)
	}
	stored, found, err = MatchProjectPath(db, root)
	if err != nil || !found {
		t.Fatalf("MatchProjectPath (absolute row) = (%q, %v, %v)", stored, found, err)
	}
	if stored != root {
		t.Errorf("stored = %q, want %q", stored, root)
	}
}

// ─── RepairProjectPaths (the change scripts' data half) ─────────────────────

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

// TestRepairProjectPaths_RewritesAbsoluteRowHomeRelative is the E-2011 half:
// a ledger already repaired by E-2002 holds the PREVIOUS canonical form, and
// the same function brings it to the current one. This is what
// internal/schema/changes/e-2011-home-relative-project-paths.go runs.
func TestRepairProjectPaths_RewritesAbsoluteRowHomeRelative(t *testing.T) {
	db := withTestDB(t)
	home := withTempHome(t)
	root := filepath.Join(home, "Projects", "acme")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seedProject(t, db, 1, "acme", root)

	repair := repairInTx(t, db)

	if repair.Rewritten != 1 || repair.Merged != 0 {
		t.Errorf("repair = %+v, want {Merged:0 Rewritten:1}", repair)
	}
	if got := projectPathOf(t, db, 1); got != "~/Projects/acme" {
		t.Errorf("path = %q, want %q", got, "~/Projects/acme")
	}

	// Idempotent across BOTH changes: re-running finds it already canonical.
	if again := repairInTx(t, db); again.Rewritten != 0 || again.Merged != 0 {
		t.Errorf("second repair = %+v, want a no-op", again)
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
// there, leaving symlink components in place — and, since E-2011, turning a
// stored `~/Projects/acme` into `<cwd>/~/Projects/acme`. Every project-path
// comparison in this package must go through ResolvedProjectPath instead.
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
				"use monitor.ResolvedProjectPath", name, i+1, strings.TrimSpace(line))
		}
	}
}

// TestEveryProjectsPathReaderResolves is the E-2011 companion guard, and it
// spans the whole repo rather than this package: the tilde made `projects.path`
// a string that LOOKS usable. `filepath.Join(path, ".endless")` on `~/x`
// compiles, runs, and produces a directory that has never existed — so the
// hazard is not "did you resolve symlinks" any more, it is "did you resolve at
// all", and it lands in whichever command reads the column next.
//
// The rule it enforces: a Go file that SELECTs `projects.path` must name
// ResolvedProjectPath or StoredProjectPath somewhere in it. Deliberately
// file-granular — pinning it tighter would mean parsing Go, and the point is to
// make the omission visible, not to prove the use is correct.
//
// It scans a NORMALIZED blob, not raw lines. The first cut of this guard
// matched line by line and so saw only single-line queries; it passed while
// monitor/triage_reads.go selected `p.path` through a multi-line
// `JOIN projects p`, shipped the raw column into `endless-go event
// --project-root`, and made every triage run fail with `project root
// "~/Projects/endless" is not a git work tree`. Collapsing Go string
// concatenation and newlines first is what closes that.
func TestEveryProjectsPathReaderResolves(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// The walk root is literally "..", whose Name() starts with a dot —
			// skipping it would silently walk nothing and pass forever.
			if path == root {
				return nil
			}
			// Vendored code, and any nested worktree checkout, are not ours.
			if name := d.Name(); name == "vendor" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(src)
		if strings.Contains(text, "ResolvedProjectPath") || strings.Contains(text, "StoredProjectPath") {
			return nil
		}
		if query, found := selectsProjectsPath(text); found {
			t.Errorf("%s reads projects.path but never resolves it:\n  %s\n"+
				"a stored path is `~/…`; pass it through monitor.ResolvedProjectPath "+
				"before it touches the filesystem (E-2011)", path, query)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// selectsProjectsPath reports whether source contains a SELECT that reads the
// `path` column of `projects`, and returns the matching query for the message.
//
// Analyzing SQL means looking at the SQL, not at the Go around it. Working on
// raw source went wrong twice: the first cut matched line by line and could not
// see a query split across concatenated literals — which is exactly how
// triage_reads.go hid a `JOIN projects p` selecting `p.path`, shipped the raw
// column into `endless-go event --project-root`, and made every triage run fail
// with `project root "~/Projects/endless" is not a git work tree`. Treating the
// whole file as SQL then makes `db.Query("SELECT …")` itself look like a
// parenthesized subquery. Pulling the literals out first leaves clean SQL.
//
// Each SELECT's window ends at the next SELECT, so a neighbouring query cannot
// lend it a `projects` it does not reference; subqueries are flattened away
// first so that cut lands between statements rather than inside one.
func selectsProjectsPath(source string) (string, bool) {
	sql := flattenSubqueries(collapseSpace(sqlLiterals(source)))
	upper := strings.ToUpper(sql)
	starts := []int{}
	for i := 0; ; {
		j := strings.Index(upper[i:], "SELECT ")
		if j < 0 {
			break
		}
		starts = append(starts, i+j)
		i += j + len("SELECT ")
	}
	for i, sel := range starts {
		end := len(sql)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		window := sql[sel:end]
		windowUpper := upper[sel:end]
		if !strings.Contains(windowUpper, "FROM PROJECTS") &&
			!strings.Contains(windowUpper, "JOIN PROJECTS") {
			continue
		}
		from := strings.Index(windowUpper, " FROM ")
		if from < 0 {
			continue
		}
		for _, col := range strings.Split(window[len("SELECT "):from], ",") {
			col = strings.ToLower(strings.TrimSpace(col))
			if col == "path" || strings.HasSuffix(col, ".path") ||
				col == "*" || strings.HasSuffix(col, ".*") {
				if len(window) > 120 {
					window = window[:120]
				}
				return strings.TrimSpace(window), true
			}
		}
	}
	return "", false
}

// sqlLiterals returns every string-literal body in source, joined by spaces, so
// a statement split across concatenated literals reads as one statement.
// Comments are skipped — prose about a query is not a query. Two unrelated
// literals may run together, which can only over-report; over-reporting costs
// an import, under-reporting costs a shipped bug.
func sqlLiterals(source string) string {
	var out strings.Builder
	for i := 0; i < len(source); {
		switch {
		case strings.HasPrefix(source[i:], "//"):
			j := strings.IndexByte(source[i:], '\n')
			if j < 0 {
				return out.String()
			}
			i += j
		case strings.HasPrefix(source[i:], "/*"):
			j := strings.Index(source[i+2:], "*/")
			if j < 0 {
				return out.String()
			}
			i += 2 + j + 2
		case source[i] == '`':
			j := strings.IndexByte(source[i+1:], '`')
			if j < 0 {
				return out.String()
			}
			out.WriteString(source[i+1 : i+1+j])
			out.WriteByte(' ')
			i += 1 + j + 1
		case source[i] == '"' || source[i] == '\'':
			quote := source[i]
			j := i + 1
			for j < len(source) && source[j] != quote && source[j] != '\n' {
				if source[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(source) && source[j] == quote {
				out.WriteString(source[i+1 : j])
				out.WriteByte(' ')
			}
			i = j + 1
		default:
			i++
		}
	}
	return out.String()
}

// collapseSpace reduces every run of whitespace to a single space.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// flattenSubqueries flattens parentheses, deleting any group that contains its
// own SELECT. Innermost group first, and non-SELECT groups are unwrapped rather
// than dropped, so a subquery containing a function call
// (`(SELECT count(*) FROM …)`) becomes innermost in turn and is deleted whole.
// Matching SELECT-bearing groups directly would miss exactly that one, which is
// the shape actually in the tree.
func flattenSubqueries(sql string) string {
	for {
		open := -1
		closed := -1
		for i := 0; i < len(sql); i++ {
			switch sql[i] {
			case '(':
				open = i
			case ')':
				closed = i
			}
			if closed >= 0 {
				break
			}
		}
		if open < 0 || closed < 0 || closed < open {
			return sql
		}
		body := sql[open+1 : closed]
		if strings.Contains(strings.ToUpper(body), "SELECT") {
			body = ""
		}
		sql = sql[:open] + " " + body + " " + sql[closed+1:]
	}
}
