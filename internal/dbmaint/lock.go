// Package dbmaint holds the two pieces of database maintenance state that every
// Endless opener has to be able to read without linking the application: the
// maintenance lock, and the record linking a schema migration to the backup
// taken just before it (E-2397).
//
// It is a leaf. It imports the standard library and nothing else, because the
// migration-only executable (cmd/endless-migrate) honors the lock too, and its
// build may link nothing that expects a schema.
//
// # Why a lock
//
// On 2026-10-18 a `db restore` silently produced an EMPTY database. In the gap
// between parking the old file and copying the backup in, the tmux status line
// (which polls every two seconds) opened the empty path, and SQLite created a
// fresh database there. No dry run can see an opener that brief, and killing
// the long-lived ones by hand only clears the openers that already exist.
//
// The lock closes that from the other side: a file beside the database that
// every opener checks before it opens anything, and that makes it refuse,
// quietly, while it is set. `endless db recover` and `endless db restore` set
// it for the length of the swap.
//
// # A lock nobody holds is no lock
//
// The lock records the pid that took it. An opener treats a lock whose process
// has exited as absent, so a recover that was killed outright cannot lock every
// session out of Endless until someone finds the file. The process holding the
// lock is exempt from it, so the command doing the maintenance can still open
// the database it is maintaining.
package dbmaint

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// LockSuffix names the lock file beside the database it guards:
// endless.db -> endless.db.maintenance.
const LockSuffix = ".maintenance"

// LockPath is the lock file for the database at dbPath.
func LockPath(dbPath string) string {
	return dbPath + LockSuffix
}

// Lock is the lock file's content.
type Lock struct {
	PID     int       `json:"pid"`
	Command string    `json:"command"`
	Started time.Time `json:"started"`
}

// ErrMaintenance is matched (errors.Is) by every refusal an opener raises
// because the lock is set.
var ErrMaintenance = errors.New("the Endless database is under maintenance")

// Error is the refusal an opener returns while the lock is set. It names who
// holds it and since when, so a person reading it knows what they are waiting
// for, and that it is not a fault.
type Error struct {
	DBPath string
	Lock   Lock
}

func (e *Error) Error() string {
	who := e.Lock.Command
	if who == "" {
		who = "maintenance"
	}
	since := ""
	if !e.Lock.Started.IsZero() {
		since = " since " + e.Lock.Started.Local().Format("15:04:05")
	}
	return fmt.Sprintf("the Endless database is under maintenance (%s, pid %d%s); "+
		"nothing was opened. It clears when that command finishes.",
		who, e.Lock.PID, since)
}

// Is makes every *Error match ErrMaintenance.
func (e *Error) Is(target error) bool { return target == ErrMaintenance }

// Active reports the lock on the database at dbPath, if one is in force for
// this process: present, held by a process that is still alive, and not held
// by this process itself.
//
// A lock file that cannot be parsed is treated as in force. Acquire writes the
// whole file before it becomes visible, so a garbled one was written by hand,
// and refusing is the conservative reading; the refusal names the file.
func Active(dbPath string) (lock Lock, active bool) {
	data, err := os.ReadFile(LockPath(dbPath))
	if err != nil {
		return Lock{}, false
	}
	if json.Unmarshal(data, &lock) != nil {
		return Lock{Command: "unreadable lock file " + LockPath(dbPath)}, true
	}
	if lock.PID == os.Getpid() {
		return lock, false
	}
	return lock, Alive(lock.PID)
}

// Check returns an *Error when the lock is in force for the database at dbPath,
// and nil otherwise. It is what every opener calls before it opens.
func Check(dbPath string) error {
	lock, active := Active(dbPath)
	if !active {
		return nil
	}
	return &Error{DBPath: dbPath, Lock: lock}
}

// Alive reports whether pid names a running process. kill(pid, 0) sends no
// signal; EPERM means the process exists under another user, which is alive.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// ErrHeld is returned by Acquire when another live process holds the lock.
var ErrHeld = errors.New("the database maintenance lock is held")

// Acquire takes the lock on the database at dbPath for this process, naming
// command as the holder. It replaces a lock whose holder has exited, and
// refuses one whose holder is alive.
//
// The file is written whole under a temporary name and hard-linked into place,
// which fails if the name exists: an opener can never read a half-written
// lock, and two acquirers cannot both win.
//
// release removes the lock, and only if it is still this process's own.
func Acquire(dbPath, command string) (release func() error, err error) {
	var data []byte
	var tmp *os.File
	var tmpName string
	path := LockPath(dbPath)

	data, err = json.Marshal(Lock{PID: os.Getpid(), Command: command, Started: time.Now()})
	if err != nil {
		goto end
	}
	tmp, err = os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		err = fmt.Errorf("creating the maintenance lock beside %s: %w", dbPath, err)
		goto end
	}
	tmpName = tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		err = fmt.Errorf("writing the maintenance lock: %w", err)
		goto end
	}

	for attempt := 0; attempt < 2; attempt++ {
		err = os.Link(tmpName, path)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			err = fmt.Errorf("taking the maintenance lock %s: %w", path, err)
			goto end
		}
		held, active := Active(dbPath)
		if active || held.PID == os.Getpid() {
			err = fmt.Errorf("%w by %s (pid %d), at %s",
				ErrHeld, held.Command, held.PID, path)
			goto end
		}
		// Its holder has exited: the lock is stale, and taking it over is
		// what keeps a killed recover from locking Endless forever.
		_ = os.Remove(path)
	}
	if err != nil {
		err = fmt.Errorf("taking the maintenance lock %s: %w", path, err)
		goto end
	}
	release = func() error { return releaseOwn(path) }

end:
	return release, err
}

// releaseOwn removes the lock at path if this process holds it. A lock that is
// missing, or that another process has since taken over, is left alone.
func releaseOwn(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var lock Lock
	if json.Unmarshal(data, &lock) == nil && lock.PID != os.Getpid() {
		return nil
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
