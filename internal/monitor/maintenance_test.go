package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/dbmaint"
	"github.com/mikeschinkel/endless/internal/schema"
)

// E-2397: the maintenance lock at the application's connect, the linked
// pre-migration backup, and the retention that keeps it.

// holdLockAs writes a maintenance lock for the database at dbPath held by pid,
// as another process's lock looks to this one. os.Getppid() is the `go test`
// driver: alive for the whole test and never this process.
func holdLockAs(t *testing.T, dbPath string, pid int) {
	t.Helper()
	data, err := json.Marshal(dbmaint.Lock{PID: pid, Command: "endless db recover", Started: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(dbmaint.LockPath(dbPath), data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(dbmaint.LockPath(dbPath)) })
}

func TestDB_UnderTheLockRefusesWithoutCreatingADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endless.db")
	holdLockAs(t, path, os.Getppid())

	_, err := openDBAtPath(t, path)

	if !UnderMaintenance(err) {
		t.Fatalf("DB() under the lock = %v, want the maintenance refusal", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("DB() under the lock created %s", path)
	}
}

func TestDB_LetsGoOfItsConnectionUnderTheLockAndReconnectsAfter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endless.db")
	buildAt(t, path, 0)
	first, err := openDBAtPath(t, path)
	if err != nil {
		t.Fatal(err)
	}

	holdLockAs(t, path, os.Getppid())
	if _, err = DB(); !UnderMaintenance(err) {
		t.Fatalf("DB() after the lock was taken = %v, want the maintenance refusal", err)
	}
	if first.Ping() == nil {
		t.Error("the long-lived connection is still open under the lock; a monitor would keep the file")
	}

	_ = os.Remove(dbmaint.LockPath(path))
	second, err := DB()
	if err != nil {
		t.Fatalf("DB() after the lock cleared = %v", err)
	}
	if err = second.Ping(); err != nil {
		t.Errorf("the reconnected handle does not answer: %v", err)
	}
}

func TestDB_IgnoresALockWhoseHolderHasExited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endless.db")
	buildAt(t, path, 0)
	holdLockAs(t, path, deadPID(t))

	if _, err := openDBAtPath(t, path); err != nil {
		t.Fatalf("DB() with a stale lock = %v, want it ignored", err)
	}
}

func TestBackupDB_RefusesUnderTheLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endless.db")
	buildAt(t, path, 0)
	resetDBSingleton(t)
	dbContextDir = filepath.Dir(path)
	holdLockAs(t, path, os.Getppid())

	_, err := BackupDB()

	if !UnderMaintenance(err) {
		t.Fatalf("BackupDB() under the lock = %v, want the maintenance refusal", err)
	}
}

// TestConnect_BehindLinksAFreshPreMigrationBackup: the migration a connect
// applies records the backup it stands on, and that backup is written NOW even
// though a backup inside the throttle window already exists.
func TestConnect_BehindLinksAFreshPreMigrationBackup(t *testing.T) {
	latest, err := schema.LatestVersion()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "endless.db")
	buildAt(t, path, latest-1)
	backups := filepath.Join(filepath.Dir(path), "backups")
	if err = os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	recent := backupPrefix + time.Now().Add(-20*time.Second).Format(backupStampLayout) + backupSuffix
	if err = os.WriteFile(filepath.Join(backups, recent), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err = openDBAtPath(t, path); err != nil {
		t.Fatalf("connect: %v", err)
	}

	links, err := dbmaint.ReadLinks(backups)
	if err != nil || len(links) != 1 {
		t.Fatalf("links = %v (err %v), want exactly one", links, err)
	}
	link := links[0]
	if link.From != latest-1 || link.To != latest {
		t.Errorf("link versions = %d -> %d, want %d -> %d", link.From, link.To, latest-1, latest)
	}
	if link.Backup == recent {
		t.Errorf("the link names the throttled backup %s, not a fresh one", recent)
	}
	if got := dbVersionAt(t, filepath.Join(backups, link.Backup)); got != latest-1 {
		t.Errorf("the linked backup is at version %d, want the pre-migration %d", got, latest-1)
	}
}

// TestPruneBackups_KeepsALinkedBackupOverANewerOneInItsHour: the scheduled
// backup that wins the hour must not prune the copy recovery restores.
func TestPruneBackups_KeepsALinkedBackupOverANewerOneInItsHour(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 18, 9, 59, 0, 0, time.Local)
	linked := backupPrefix + "20261018-091500" + backupSuffix
	newer := backupPrefix + "20261018-094500" + backupSuffix
	for _, name := range []string{linked, newer} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := dbmaint.AppendLink(dir, dbmaint.Link{At: now.Add(-44 * time.Minute), Backup: linked, From: 30, To: 31})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = pruneBackups(dir, now); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{linked, newer} {
		if _, statErr := os.Stat(filepath.Join(dir, name)); statErr != nil {
			t.Errorf("%s was pruned", name)
		}
	}
}
