package dbrecover

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/dbmaint"
	"github.com/mikeschinkel/endless/internal/schema"
)

// E-2397's recovery, against real databases on disk.

func exec1(t *testing.T, path, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// fixture is a config directory holding a database one schema version ahead
// of the binary, a pre-migration backup linked to that migration, and a NEWER
// scheduled backup taken after it.
type fixture struct {
	db, backups, park, linked, newer string
	latest                           int64
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	f := fixture{
		db:      filepath.Join(dir, "endless.db"),
		backups: filepath.Join(dir, "backups"),
		park:    filepath.Join(dir, "pre-restore"),
	}
	var err error
	f.latest, err = schema.LatestVersion()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", f.db)
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err = os.MkdirAll(f.backups, 0o755); err != nil {
		t.Fatal(err)
	}

	f.linked = filepath.Join(f.backups, "endless-20261018-051458.db")
	exec1(t, f.db, "VACUUM INTO ?", f.linked)
	if err = dbmaint.AppendLink(f.backups, dbmaint.Link{
		At: time.Now(), Backup: filepath.Base(f.linked), From: f.latest, To: f.latest + 1,
	}); err != nil {
		t.Fatal(err)
	}
	// The unwanted migration, then a scheduled backup that carries it.
	exec1(t, f.db, "INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)", f.latest+1)
	exec1(t, f.db, "INSERT INTO projects (name, path) VALUES ('after', '/after')")
	f.newer = filepath.Join(f.backups, "endless-20261018-060000.db")
	exec1(t, f.db, "VACUUM INTO ?", f.newer)
	return f
}

func (f fixture) opts() Options {
	return Options{
		DBPath: f.db, BackupsDir: f.backups, ParkDir: f.park,
		BinaryVersion: f.latest, Wait: time.Second, Poll: 50 * time.Millisecond,
		Stamp: "20261018-070000", Command: "endless db recover",
	}
}

func versionAt(t *testing.T, path string) int64 {
	t.Helper()
	v, err := Version(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// P1: against a database at the installed version there is nothing to
// recover, and nothing changes.
func TestRecover_CurrentChangesNothing(t *testing.T) {
	f := newFixture(t)
	opts := f.opts()
	opts.BinaryVersion = f.latest + 1
	before, _ := os.ReadFile(f.db)

	plan, err := Recover(context.Background(), opts)

	if err != nil || plan.Status != "current" {
		t.Fatalf("Recover = %+v, %v; want status current", plan, err)
	}
	after, _ := os.ReadFile(f.db)
	if string(before) != string(after) {
		t.Error("a current database was changed")
	}
	if _, statErr := os.Stat(f.park); !os.IsNotExist(statErr) {
		t.Error("a current database was parked")
	}
}

// P1 + P2: one version ahead, both versions are named, and the backup chosen
// is the linked pre-migration one even though a newer backup exists.
func TestRecover_DryRunNamesBothVersionsAndTheLinkedBackup(t *testing.T) {
	f := newFixture(t)
	opts := f.opts()
	opts.DryRun = true

	plan, err := Recover(context.Background(), opts)

	if err != nil {
		t.Fatal(err)
	}
	if plan.DBVersion != f.latest+1 || plan.BinaryVersion != f.latest {
		t.Errorf("versions = db %d, binary %d; want %d and %d",
			plan.DBVersion, plan.BinaryVersion, f.latest+1, f.latest)
	}
	if plan.Backup != f.linked {
		t.Errorf("backup = %s, want the linked %s (not the newer %s)", plan.Backup, f.linked, f.newer)
	}
	if versionAt(t, f.db) != f.latest+1 {
		t.Error("a dry run changed the database")
	}
}

// P5 + P6: the restore lands the linked backup, verifies it, and parks the
// replaced database.
func TestRecover_RestoresAndVerifies(t *testing.T) {
	f := newFixture(t)

	plan, err := Recover(context.Background(), f.opts())

	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "recovered" || plan.Check == nil || len(plan.Check.Problems()) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if v := versionAt(t, f.db); v != f.latest {
		t.Errorf("restored version = %d, want %d", v, f.latest)
	}
	if len(plan.Parked) == 0 || versionAt(t, plan.Parked[0]) != f.latest+1 {
		t.Errorf("parked = %v, want the replaced database", plan.Parked)
	}
	if _, statErr := os.Stat(dbmaint.LockPath(f.db)); !os.IsNotExist(statErr) {
		t.Error("the lock survived a successful recovery")
	}
}

// P2: no link, no guess.
func TestRecover_WithoutALinkRefusesRatherThanTakeTheNewest(t *testing.T) {
	f := newFixture(t)
	_ = os.Remove(filepath.Join(f.backups, dbmaint.LinksFile))

	_, err := Recover(context.Background(), f.opts())

	if err == nil || !strings.Contains(err.Error(), "No backup is linked") {
		t.Fatalf("Recover without a link = %v", err)
	}
	if versionAt(t, f.db) != f.latest+1 {
		t.Error("the database changed")
	}
}

// P4: a restore that fails partway clears the lock, and the live database is
// whole.
func TestRecover_FailurePartwayClearsTheLock(t *testing.T) {
	f := newFixture(t)
	prev := beforeRename
	beforeRename = func() error { return errors.New("injected failure") }
	t.Cleanup(func() { beforeRename = prev })

	_, err := Recover(context.Background(), f.opts())

	if err == nil || !strings.Contains(err.Error(), "pre-restore database is intact") {
		t.Fatalf("Recover = %v, want the partial-failure refusal naming the parked copy", err)
	}
	if _, statErr := os.Stat(dbmaint.LockPath(f.db)); !os.IsNotExist(statErr) {
		t.Error("the lock survived a failed recovery")
	}
	if versionAt(t, f.db) != f.latest+1 {
		t.Error("the failed recovery left the live database changed")
	}
}

// P4: a handle that outlives the wait is named, by pid, and the lock clears.
func TestRecover_NamesAHeldHandleAndClearsTheLock(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("needs lsof")
	}
	f := newFixture(t)
	holder := exec.Command("sh", "-c", "exec 3<\"$0\"; sleep 30", f.db)
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		hs, _ := Holders(context.Background(), []string{f.db})
		if len(hs) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	_, err := Recover(context.Background(), f.opts())

	if err == nil || !strings.Contains(err.Error(), "still have the database open") {
		t.Fatalf("Recover with a holder = %v", err)
	}
	plan, _ := Recover(context.Background(), Options{
		DBPath: f.db, BackupsDir: f.backups, ParkDir: f.park,
		BinaryVersion: f.latest, DryRun: true,
	})
	found := false
	for _, h := range plan.Holders {
		found = found || h.PID == holder.Process.Pid
	}
	if !found {
		t.Errorf("holders = %v, want pid %d named", plan.Holders, holder.Process.Pid)
	}
	if _, statErr := os.Stat(dbmaint.LockPath(f.db)); !os.IsNotExist(statErr) {
		t.Error("the lock survived the refusal")
	}
}

// P5: during the swap the database path is never empty, and the lock is in
// force for every other process.
func TestInstall_ThePathIsNeverEmpty(t *testing.T) {
	f := newFixture(t)
	release, err := dbmaint.Acquire(f.db, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()

	stop := make(chan struct{})
	missing := make(chan bool, 1)
	go func() {
		saw := false
		for {
			select {
			case <-stop:
				missing <- saw
				return
			default:
			}
			if info, statErr := os.Stat(f.db); statErr != nil || info.Size() == 0 {
				saw = true
			}
		}
	}()
	prev := beforeRename
	beforeRename = func() error {
		lock, _ := dbmaint.Active(f.db)
		if lock.PID != os.Getpid() || !dbmaint.Alive(lock.PID) {
			t.Errorf("mid-swap lock = %+v, want this live process", lock)
		}
		return nil
	}
	t.Cleanup(func() { beforeRename = prev })

	_, err = Install(f.linked, f.db, f.park, "20261018-070000")
	close(stop)

	if err != nil {
		t.Fatal(err)
	}
	if <-missing {
		t.Error("the database path was missing or empty during the install")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, statErr := os.Stat(f.db + suffix); statErr == nil {
			t.Errorf("a stale %s survived beside the restored file", suffix)
		}
	}
}

// P6: a restored copy that differs from its backup is a failure.
func TestVerify_ReportsARestoredCopyThatDiffersFromItsBackup(t *testing.T) {
	f := newFixture(t)
	if _, err := Install(f.linked, f.db, f.park, "20261018-070000"); err != nil {
		t.Fatal(err)
	}
	exec1(t, f.db, "INSERT INTO projects (name, path) VALUES ('drift', '/drift')")

	c, err := Verify(context.Background(), f.db, f.linked)

	if err != nil {
		t.Fatal(err)
	}
	problems := strings.Join(c.Problems(), "; ")
	if !strings.Contains(problems, "table projects") {
		t.Errorf("problems = %q, want the projects row count named", problems)
	}
}

// P7: with a landable branch carrying the migration, the output names its land
// command.
func TestRecover_NamesTheLandCommandForALandableBranch(t *testing.T) {
	f := newFixture(t)
	worktree := filepath.Join(t.TempDir(), "proj", ".endless", "worktrees", "e-4242")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	exec1(t, f.db, "INSERT INTO projects (id, name, path) VALUES (900, 'p', '/p')")
	exec1(t, f.db, "INSERT INTO tasks (id, project_id, title, status) VALUES (4242, 900, 't', 'unlanded')")
	_ = os.Remove(filepath.Join(f.backups, dbmaint.LinksFile))
	if err := dbmaint.AppendLink(f.backups, dbmaint.Link{
		At: time.Now(), Backup: filepath.Base(f.linked), From: f.latest, To: f.latest + 1,
		Binary: "/tmp/pytest-of-x/endless-go", Cwd: worktree + "/tests",
	}); err != nil {
		t.Fatal(err)
	}
	opts := f.opts()
	opts.DryRun = true

	plan, err := Recover(context.Background(), opts)

	if err != nil {
		t.Fatal(err)
	}
	if plan.Land == nil || !plan.Land.Landable || plan.Land.Command != "endless worktree land E-4242" {
		t.Errorf("land advice = %+v, want landable E-4242 with its land command", plan.Land)
	}
}
