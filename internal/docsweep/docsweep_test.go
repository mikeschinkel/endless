package docsweep

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// The sweep's job is to make one claim true of a repository: every mirror is in
// the consolidated place and says exactly what its column says. These tests
// state that claim from both ends — a tree that is already right must not be
// touched, and every way a tree can be wrong must converge in one pass.

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo returns an initialized git repository with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustGit(t, root, "init", "-q", "-b", "main")
	mustGit(t, root, "config", "user.email", "test@example.com")
	mustGit(t, root, "config", "user.name", "Test")
	mustGit(t, root, "config", "commit.gpgsign", "false")
	write(t, filepath.Join(root, "README"), "hi\n")
	mustGit(t, root, "add", "README")
	mustGit(t, root, "commit", "-q", "-m", "init")
	return root
}

// newDB returns a schema'd database bound as monitor's singleton, holding one
// project rooted at root.
func newDB(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "endless.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err = db.Exec(
		"INSERT INTO projects (id, name, path, status) VALUES (1, 'p', ?, 'active')",
		root,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(monitor.SetTestDB(db))
	return db
}

func seedTask(t *testing.T, db *sql.DB, id int64, plan, outcome, analysis string) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status, plan, outcome, analysis) "+
			"VALUES (?, 1, 'title', 'ready', ?, ?, ?)",
		id, nullable(plan), nullable(outcome), nullable(analysis))
	if err != nil {
		t.Fatalf("seed task %d: %v", id, err)
	}
}

func seedDecision(t *testing.T, db *sql.DB, id int64, body string) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO decisions (id, project_id, title, description, status) "+
			"VALUES (?, 1, 'title', ?, 'proposed')", id, nullable(body))
	if err != nil {
		t.Fatalf("seed decision %d: %v", id, err)
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func write(t *testing.T, abs, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
}

func read(t *testing.T, abs string) string {
	t.Helper()
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", abs, err)
	}
	return string(b)
}

func exists(abs string) bool {
	_, err := os.Stat(abs)
	return err == nil
}

func sweep(t *testing.T, root string) Result {
	t.Helper()
	result, err := SweepProject(context.Background(), monitor.ProjectRef{ID: 1, Root: root})
	if err != nil {
		t.Fatalf("SweepProject: %v", err)
	}
	return result
}

// --- relocation -------------------------------------------------------------

func TestLegacyMirrorsMoveIntoTheTaskDirectory(t *testing.T) {
	root := newRepo(t)
	db := newDB(t, root)
	seedTask(t, db, 7, "PLAN\n", "OUTCOME\n", "ANALYSIS\n")
	write(t, filepath.Join(root, ".endless/plans/E-7.md"), "PLAN\n")
	write(t, filepath.Join(root, ".endless/outcomes/E-7.md"), "OUTCOME\n")
	write(t, filepath.Join(root, ".endless/analyses/E-7.md"), "ANALYSIS\n")
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-q", "-m", "legacy layout")

	result := sweep(t, root)

	if result.Relocated != 3 {
		t.Errorf("Relocated = %d, want 3 (%s)", result.Relocated, result)
	}
	for stem, want := range map[string]string{
		"plan": "PLAN\n", "outcome": "OUTCOME\n", "analysis": "ANALYSIS\n",
	} {
		got := read(t, filepath.Join(root, ".endless/tasks/e-7", stem+".md"))
		if got != want {
			t.Errorf("%s.md = %q, want %q", stem, got, want)
		}
	}
	for _, dir := range []string{"plans", "outcomes", "analyses"} {
		entries, _ := os.ReadDir(filepath.Join(root, ".endless", dir))
		if len(entries) != 0 {
			t.Errorf(".endless/%s still holds %d file(s)", dir, len(entries))
		}
	}
}

// TestRelocationIsCommitted pins that the move reaches git, not just the
// filesystem. A relocation left uncommitted would make every checkout of the
// project permanently dirty — the failure E-1525 removed.
func TestRelocationIsCommitted(t *testing.T) {
	root := newRepo(t)
	db := newDB(t, root)
	seedTask(t, db, 7, "PLAN\n", "", "")
	write(t, filepath.Join(root, ".endless/plans/E-7.md"), "PLAN\n")
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-q", "-m", "legacy layout")

	sweep(t, root)

	if got := mustGit(t, root, "status", "--porcelain"); got != "" {
		t.Errorf("working tree dirty after the sweep:\n%s", got)
	}
	subject := mustGit(t, root, "log", "-1", "--format=%s")
	if !strings.Contains(subject, "consolidate") {
		t.Errorf("commit subject = %q, want it to name the consolidation", subject)
	}
}

// TestLegacyLosesToTheConsolidatedCopy covers the straggler that arrives AFTER
// a task's mirror already moved: a worktree running older code lands its
// old-path file onto a main that already has the new one. The old one is the
// duplicate, and content comes from the column regardless.
func TestLegacyLosesToTheConsolidatedCopy(t *testing.T) {
	root := newRepo(t)
	db := newDB(t, root)
	seedTask(t, db, 7, "CURRENT\n", "", "")
	write(t, filepath.Join(root, ".endless/tasks/e-7/plan.md"), "CURRENT\n")
	write(t, filepath.Join(root, ".endless/plans/E-7.md"), "STALE\n")
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-q", "-m", "both layouts")

	result := sweep(t, root)

	if result.Superseded != 1 {
		t.Errorf("Superseded = %d, want 1 (%s)", result.Superseded, result)
	}
	if exists(filepath.Join(root, ".endless/plans/E-7.md")) {
		t.Error("the legacy duplicate survived")
	}
	if got := read(t, filepath.Join(root, ".endless/tasks/e-7/plan.md")); got != "CURRENT\n" {
		t.Errorf("plan.md = %q, want the column's content", got)
	}
}

// TestAStrangerInTheLegacyDirectoryIsLeftAlone: the sweep moves mirrors, not
// whatever else a project keeps in those directories.
func TestAStrangerInTheLegacyDirectoryIsLeftAlone(t *testing.T) {
	root := newRepo(t)
	newDB(t, root)
	write(t, filepath.Join(root, ".endless/plans/README.md"), "how we plan\n")
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-q", "-m", "a readme")

	result := sweep(t, root)

	if result.Total() != 0 {
		t.Errorf("sweep touched %d path(s) (%s); want none", result.Total(), result)
	}
	if !exists(filepath.Join(root, ".endless/plans/README.md")) {
		t.Error("the README was moved")
	}
}

// --- reconciliation ---------------------------------------------------------

// TestMissingMirrorsAreCreated is the backfill this task needs: every mirror
// for a task that had a worktree used to live on that worktree's branch and
// never reached main at all.
func TestMissingMirrorsAreCreated(t *testing.T) {
	root := newRepo(t)
	db := newDB(t, root)
	seedTask(t, db, 42, "THE PLAN\n", "", "")
	seedDecision(t, db, 9, "THE DECISION\n")

	result := sweep(t, root)

	if result.Created != 2 {
		t.Errorf("Created = %d, want 2 (%s)", result.Created, result)
	}
	if got := read(t, filepath.Join(root, ".endless/tasks/e-42/plan.md")); got != "THE PLAN\n" {
		t.Errorf("plan.md = %q", got)
	}
	if got := read(t, filepath.Join(root, ".endless/decisions/ED-9.md")); got != "THE DECISION\n" {
		t.Errorf("ED-9.md = %q", got)
	}
}

// TestDriftedMirrorsAreRewritten is the repair half. The mirror write is
// best-effort by design — a failed commit warns rather than failing the command
// — so something has to come back for it.
func TestDriftedMirrorsAreRewritten(t *testing.T) {
	root := newRepo(t)
	db := newDB(t, root)
	seedTask(t, db, 42, "THE PLAN\n", "", "")
	write(t, filepath.Join(root, ".endless/tasks/e-42/plan.md"), "CORRUPTED BY HAND\n")
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-q", "-m", "a hand edit")

	result := sweep(t, root)

	if result.Rewritten != 1 {
		t.Errorf("Rewritten = %d, want 1 (%s)", result.Rewritten, result)
	}
	if got := read(t, filepath.Join(root, ".endless/tasks/e-42/plan.md")); got != "THE PLAN\n" {
		t.Errorf("plan.md = %q, want the column's content", got)
	}
}

// TestAMatchingMirrorIsNotRewritten is what makes a converged repository quiet.
// A sweep that rewrote identical content would commit on main every fifteen
// minutes forever.
func TestAMatchingMirrorIsNotRewritten(t *testing.T) {
	root := newRepo(t)
	db := newDB(t, root)
	seedTask(t, db, 42, "THE PLAN\n", "", "")
	write(t, filepath.Join(root, ".endless/tasks/e-42/plan.md"), "THE PLAN\n")
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-q", "-m", "already right")
	head := mustGit(t, root, "rev-parse", "HEAD")

	result := sweep(t, root)

	if result.Total() != 0 {
		t.Errorf("sweep touched %d path(s) (%s); want none", result.Total(), result)
	}
	if mustGit(t, root, "rev-parse", "HEAD") != head {
		t.Error("the sweep committed over an already-correct tree")
	}
}

// TestAnEmptyColumnNeverOverwritesAFile is the one asymmetry that keeps this
// sweep from ever losing content. "Regenerating a derived file is free" holds
// only when there is something to regenerate.
func TestAnEmptyColumnNeverOverwritesAFile(t *testing.T) {
	root := newRepo(t)
	db := newDB(t, root)
	seedTask(t, db, 42, "", "", "")
	write(t, filepath.Join(root, ".endless/tasks/e-42/plan.md"), "CONTENT THE DB LOST\n")
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-q", "-m", "orphaned content")

	result := sweep(t, root)

	if result.Total() != 0 {
		t.Errorf("sweep touched %d path(s) (%s); want none", result.Total(), result)
	}
	got := read(t, filepath.Join(root, ".endless/tasks/e-42/plan.md"))
	if got != "CONTENT THE DB LOST\n" {
		t.Errorf("plan.md = %q; an empty column erased a non-empty file", got)
	}
}

// TestARemovedTaskIsNotMirrored: `task remove` retains the row with removed=1,
// and reads go through live_tasks. A sweep that read `tasks` directly would
// resurrect mirrors for removed tasks on every pass.
func TestARemovedTaskIsNotMirrored(t *testing.T) {
	root := newRepo(t)
	db := newDB(t, root)
	seedTask(t, db, 42, "THE PLAN\n", "", "")
	if _, err := db.Exec("UPDATE tasks SET removed = 1 WHERE id = 42"); err != nil {
		t.Fatalf("remove task: %v", err)
	}

	result := sweep(t, root)

	if result.Total() != 0 {
		t.Errorf("sweep touched %d path(s) (%s); want none", result.Total(), result)
	}
	if exists(filepath.Join(root, ".endless/tasks/e-42/plan.md")) {
		t.Error("a removed task got a mirror")
	}
}

// --- the two halves together ------------------------------------------------

// TestRelocationRunsBeforeReconciliation pins the ordering. Reconciling first
// would CREATE a file at the consolidated path while the legacy file still sat
// there, turning every relocation into a supersede and losing the rename git
// would otherwise record.
func TestRelocationRunsBeforeReconciliation(t *testing.T) {
	root := newRepo(t)
	db := newDB(t, root)
	seedTask(t, db, 7, "PLAN\n", "", "")
	write(t, filepath.Join(root, ".endless/plans/E-7.md"), "PLAN\n")
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-q", "-m", "legacy layout")

	result := sweep(t, root)

	if result.Relocated != 1 || result.Created != 0 || result.Superseded != 0 {
		t.Errorf("result = %s; want exactly one relocation", result)
	}
	names := mustGit(t, root, "show", "--name-status", "--find-renames", "--format=", "HEAD")
	if !strings.Contains(names, "R") {
		t.Errorf("the commit does not read as a rename:\n%s", names)
	}
}

// TestSweepIsIdempotent is the lease contract from internal/jobs: a slow run can
// be re-claimed and run concurrently, so a second pass must find nothing to do.
func TestSweepIsIdempotent(t *testing.T) {
	root := newRepo(t)
	db := newDB(t, root)
	seedTask(t, db, 7, "PLAN\n", "OUT\n", "")
	seedTask(t, db, 8, "PLAN 8\n", "", "")
	seedDecision(t, db, 3, "BODY\n")
	write(t, filepath.Join(root, ".endless/plans/E-7.md"), "PLAN\n")
	write(t, filepath.Join(root, ".endless/analyses/E-8.md"), "ORPHANED ANALYSIS\n")
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-q", "-m", "mixed")

	first := sweep(t, root)
	if first.Total() == 0 {
		t.Fatal("the first pass did nothing; the fixture proves nothing")
	}
	head := mustGit(t, root, "rev-parse", "HEAD")

	second := sweep(t, root)

	if second.Total() != 0 {
		t.Errorf("second pass touched %d path(s) (%s); want none", second.Total(), second)
	}
	if mustGit(t, root, "rev-parse", "HEAD") != head {
		t.Error("the second pass committed")
	}
	if got := mustGit(t, root, "status", "--porcelain"); got != "" {
		t.Errorf("working tree dirty after two passes:\n%s", got)
	}
}

// TestAnUntrackedLegacyMirrorRelocatesWithoutFailing is WARN-0001 incident 1534,
// at the level it actually happened.
//
// Every fixture above commits its legacy mirrors, which is Endless's own
// situation and not the general one. A project that has not committed its
// `.endless/` tree — the default for a project that just started using Endless —
// has UNTRACKED mirrors. Relocating one leaves an old path git has never heard
// of, and staging it used to fail the commit, the sweep, and every later pass.
func TestAnUntrackedLegacyMirrorRelocatesWithoutFailing(t *testing.T) {
	root := newRepo(t)
	db := newDB(t, root)
	seedTask(t, db, 1173, "PLAN\n", "", "")
	// Written, deliberately NOT committed.
	write(t, filepath.Join(root, ".endless/plans/E-1173.md"), "PLAN\n")

	result := sweep(t, root)

	if result.Relocated != 1 {
		t.Errorf("Relocated = %d, want 1 (%s)", result.Relocated, result)
	}
	if got := read(t, filepath.Join(root, ".endless/tasks/e-1173/plan.md")); got != "PLAN\n" {
		t.Errorf("plan.md = %q", got)
	}
	if exists(filepath.Join(root, ".endless/plans/E-1173.md")) {
		t.Error("the legacy copy survived")
	}
	// And it is committed, not merely moved on disk.
	names := mustGit(t, root, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(names, ".endless/tasks/e-1173/plan.md") {
		t.Errorf("the relocated mirror was not committed; HEAD carries:\n%s", names)
	}

	// The pass must also be idempotent from this state — the failure it replaces
	// recurred on every tick.
	second := sweep(t, root)
	if second.Total() != 0 {
		t.Errorf("second pass touched %d path(s) (%s); want none", second.Total(), second)
	}
}
