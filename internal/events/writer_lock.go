package events

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// LedgerRoot returns the root directory whose .endless/db-ledger/ holds the
// event ledger for the active DB context (ED-1525: the ledger follows the DB).
// In a worktree sandbox the ledger lives beside the sandbox endless.db, under
// monitor.ConfigDir(), so sandbox emits and reads never touch the real project
// ledger; otherwise it is projectRoot, the real checkout. NewWriter and
// ReadAllEvents append .endless/db-ledger to whatever root they receive.
func LedgerRoot(projectRoot string) string {
	if monitor.IsSandboxActive() {
		return monitor.ConfigDir()
	}
	return projectRoot
}

// The per-ledger append lock (E-2486).
//
// Every Append takes an exclusive file lock around its write, so appends from
// separate processes are serialized rather than merely atomic. O_APPEND already
// kept one line from tearing another; what it could not promise is that two
// lines a single run writes end up ADJACENT, and a correction's entry records
// its change entries by byte offset, which only adjacency under a lock makes a
// stable fact. The lock is per ledger root — one file beside the segments, not
// per segment — so a rotation inside a run cannot split it.
//
// The lock file lives in `.endless/tmp/`, the project-local scratch directory
// `endless register` gitignores. In case a project's .gitignore predates that
// entry, the directory is given its own `.gitignore` of `*`, the same way a
// sandbox keeps itself out of git, so the lock never shows up as an untracked
// file on anyone's main checkout.

// LedgerLockFile is the lock's name inside the ledger root's `.endless/tmp/`.
const LedgerLockFile = "db-ledger.lock"

// ledgerLockWait bounds how long an append waits for another process to let go
// of the lock before refusing. Comfortably longer than one ledger commit, and
// short enough that a wedged holder surfaces as a refusal rather than a hang.
const ledgerLockWait = 15 * time.Second

// ledgerLockPoll is the interval between non-blocking lock attempts.
const ledgerLockPoll = 20 * time.Millisecond

// lockPath is where the writer's ledger lock lives: `.endless/tmp/db-ledger.lock`
// under the same root whose `.endless/db-ledger/` holds the segments.
func (w *Writer) lockPath() string {
	return filepath.Join(filepath.Dir(w.eventsDir), "tmp", LedgerLockFile)
}

// acquire takes the exclusive lock, creating the lock file and its
// self-ignoring directory on first use. It polls with LOCK_NB rather than
// blocking, so a holder that never returns produces a refusal naming the file
// instead of a process that cannot be interrupted cleanly.
func (w *Writer) acquire() error {
	if w.lockFD != nil {
		return nil
	}
	path := w.lockPath()
	if err := ensureSelfIgnoringDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("events: create ledger lock dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("events: open ledger lock: %w", err)
	}
	deadline := time.Now().Add(ledgerLockWait)
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			w.lockFD = f
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			f.Close()
			return fmt.Errorf("events: lock ledger: %w", err)
		}
		if time.Now().After(deadline) {
			f.Close()
			// Another process has held the ledger for longer than any append
			// or commit takes. Whether that process is stuck or merely slow is
			// something the user has to look at; retrying blind is not it.
			return refusal.Report(
				fmt.Sprintf("events: another process has held the ledger lock %s for over %s", path, ledgerLockWait),
				"whether the process holding the ledger lock is stuck, and whether to stop it").
				Cause(err)
		}
		time.Sleep(ledgerLockPoll)
	}
}

// release lets the lock go. Safe to call without holding it.
func (w *Writer) release() {
	if w.lockFD == nil {
		return
	}
	_ = unix.Flock(int(w.lockFD.Fd()), unix.LOCK_UN)
	_ = w.lockFD.Close()
	w.lockFD = nil
}

// ensureSelfIgnoringDir creates dir and a `.gitignore` of `*` inside it, so
// whatever lands there stays out of git whether or not the enclosing project's
// .gitignore names the directory.
func ensureSelfIgnoringDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ignore := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(ignore); err == nil {
		return nil
	}
	return os.WriteFile(ignore, []byte("*\n"), 0o644)
}
