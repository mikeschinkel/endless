package dbmaint

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func writeLock(t *testing.T, dbPath string, pid int) {
	t.Helper()
	data, _ := json.Marshal(Lock{PID: pid, Command: "endless db recover", Started: time.Now()})
	if err := os.WriteFile(LockPath(dbPath), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name   string
		pid    func(t *testing.T) int
		absent bool
		want   bool
	}{
		{"no lock", nil, true, false},
		{"another live process holds it", func(*testing.T) int { return os.Getppid() }, false, true},
		{"its holder has exited", deadPID, false, false},
		{"this process holds it", func(*testing.T) int { return os.Getpid() }, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "endless.db")
			if !c.absent {
				writeLock(t, db, c.pid(t))
			}
			err := Check(db)
			if got := errors.Is(err, ErrMaintenance); got != c.want {
				t.Errorf("Check = %v, want in force %v", err, c.want)
			}
		})
	}
}

func TestCheck_HonorsAnUnreadableLock(t *testing.T) {
	db := filepath.Join(t.TempDir(), "endless.db")
	if err := os.WriteFile(LockPath(db), []byte("{half"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(Check(db), ErrMaintenance) {
		t.Error("a garbled lock was ignored")
	}
}

func TestAcquire(t *testing.T) {
	t.Run("takes, then releases", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "endless.db")
		release, err := Acquire(db, "test")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(LockPath(db)); err != nil {
			t.Fatalf("no lock file after Acquire: %v", err)
		}
		if err = release(); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(LockPath(db)); !os.IsNotExist(err) {
			t.Error("the lock file survived release")
		}
	})
	t.Run("refuses a live holder", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "endless.db")
		writeLock(t, db, os.Getppid())
		if _, err := Acquire(db, "test"); !errors.Is(err, ErrHeld) {
			t.Errorf("Acquire over a live holder = %v, want ErrHeld", err)
		}
	})
	t.Run("takes over a stale lock", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "endless.db")
		writeLock(t, db, deadPID(t))
		release, err := Acquire(db, "test")
		if err != nil {
			t.Fatalf("Acquire over a stale lock = %v", err)
		}
		_ = release()
	})
	t.Run("release leaves another process's lock alone", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "endless.db")
		release, err := Acquire(db, "test")
		if err != nil {
			t.Fatal(err)
		}
		writeLock(t, db, os.Getppid())
		_ = release()
		if _, err = os.Stat(LockPath(db)); err != nil {
			t.Error("release removed a lock it no longer held")
		}
	})
}

func TestLinkFor(t *testing.T) {
	links := []Link{
		{Backup: "a", From: 28, To: 29},
		{Backup: "b", From: 29, To: 30},
		{Backup: "c", From: 29, To: 31},
	}
	cases := []struct {
		name       string
		db, binary int64
		want       string
		wantOK     bool
	}{
		{"newest link that spans the gap", 31, 29, "c", true},
		{"a link from below the binary still undoes it", 30, 29, "c", true},
		{"no link reaches that far", 32, 29, "", false},
		{"no link starts low enough", 31, 27, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := LinkFor(links, c.db, c.binary)
			if ok != c.wantOK || got.Backup != c.want {
				t.Errorf("LinkFor(db %d, binary %d) = %q %v, want %q %v",
					c.db, c.binary, got.Backup, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestLinks_AppendReadProtect(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for _, l := range []Link{
		{At: now.Add(-40 * 24 * time.Hour), Backup: "old", From: 1, To: 2},
		{At: now.Add(-time.Hour), Backup: "new", From: 2, To: 3},
	} {
		if err := AppendLink(dir, l); err != nil {
			t.Fatal(err)
		}
	}
	// A damaged line hides nothing else.
	f, _ := os.OpenFile(filepath.Join(dir, LinksFile), os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("not json\n")
	_ = f.Close()

	links, err := ReadLinks(dir)
	if err != nil || len(links) != 2 {
		t.Fatalf("ReadLinks = %v, %v; want both links", links, err)
	}
	protected := ProtectedBackups(links, now)
	if protected["old"] || !protected["new"] {
		t.Errorf("protected = %v, want only the link inside the window", protected)
	}
}
