package monitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// E-971 Layer D — worktree adoption primitives.
//
// Each endless-managed git worktree carries two files at its root:
//
//   <worktree>/.endless/worktree.json   — companion: kind, task_id, branch, etc.
//   <worktree>/.endless/worktree.lock   — ownership: which session is editing here
//
// The companion is written by `endless pivot` (Layer F) or by hand. The
// lock is written by SessionStart hook on adoption, deleted by SessionEnd.
// Filesystem is authoritative; no DB tables.

const (
	worktreeCompanionFile = "worktree.json"
	worktreeLockFile      = "worktree.lock"
	worktreeEndlessDir    = ".endless"
	worktreeWorktreesDir  = "worktrees"
)

// WorktreeCompanion is the parsed shape of <worktree>/.endless/worktree.json.
// Both kinds (task and session) deserialize through the same struct;
// per-kind fields are optional.
type WorktreeCompanion struct {
	Kind       string `json:"kind"`
	TaskID     string `json:"task_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
	Branch     string `json:"branch,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

// WorktreeLock is the parsed shape of <worktree>/.endless/worktree.lock.
// One session owns the worktree at a time; ownership transfers via release
// then re-claim, never shares.
type WorktreeLock struct {
	SessionID string `json:"session_id"`
	PID       int    `json:"pid"`
	TmuxPane  string `json:"tmux_pane,omitempty"`
	ClaimedAt string `json:"claimed_at"`
}

// ReadWorktreeCompanion reads <worktree>/.endless/worktree.json. Returns
// (nil, os.ErrNotExist) if the file is absent so callers can distinguish
// "not an endless-managed worktree" from a real read error.
func ReadWorktreeCompanion(worktreePath string) (*WorktreeCompanion, error) {
	path := filepath.Join(worktreePath, worktreeEndlessDir, worktreeCompanionFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("read worktree companion: %w", err)
	}
	var c WorktreeCompanion
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse worktree companion at %s: %w", path, err)
	}
	return &c, nil
}

// ClaimWorktreeLock atomically creates the lock file at
// <worktree>/.endless/worktree.lock with O_EXCL semantics. Returns
// os.ErrExist if a lock file already exists; callers should then read it
// and check IsWorktreeLockStale before reclaiming.
//
// The directory <worktree>/.endless must already exist (it does on any
// endless-managed worktree because the companion file lives there).
func ClaimWorktreeLock(worktreePath string, lock WorktreeLock) error {
	if lock.ClaimedAt == "" {
		lock.ClaimedAt = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal worktree lock: %w", err)
	}
	dir := filepath.Join(worktreePath, worktreeEndlessDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create worktree .endless dir: %w", err)
	}
	path := filepath.Join(dir, worktreeLockFile)
	// O_EXCL is the atomic-create primitive POSIX guarantees across
	// processes. If the file exists, OpenFile returns os.ErrExist.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return os.ErrExist
		}
		return fmt.Errorf("open worktree lock: %w", err)
	}
	if _, writeErr := f.Write(data); writeErr != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("write worktree lock: %w", writeErr)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close worktree lock: %w", err)
	}
	return nil
}

// ReadWorktreeLock reads the lock file. Returns (nil, os.ErrNotExist) if
// the lock is absent.
func ReadWorktreeLock(worktreePath string) (*WorktreeLock, error) {
	path := filepath.Join(worktreePath, worktreeEndlessDir, worktreeLockFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("read worktree lock: %w", err)
	}
	var l WorktreeLock
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("parse worktree lock at %s: %w", path, err)
	}
	return &l, nil
}

// IsWorktreeLockStale reports whether the lock's owning PID is no longer
// alive. Uses the canonical Unix idiom: kill(pid, 0) sends NO signal —
// the kernel performs only the existence and permission checks. See
// `man 2 kill`: "If sig is 0, then no signal is sent, but existence and
// permission checks are still performed."
//
// Returns:
//   - true  if the process is gone (ESRCH) or PID is invalid (≤ 0)
//   - false if the process exists (nil) or exists but isn't ours (EPERM)
//   - false on any other error (be conservative; do not steal locks
//     when we are uncertain)
//
// Platform note: this is Unix-only. Endless depends on tmux for several
// core features and so doesn't target Windows yet; on Windows the
// equivalent would be OpenProcess.
func IsWorktreeLockStale(lock *WorktreeLock) bool {
	if lock == nil || lock.PID <= 0 {
		return true
	}
	err := syscall.Kill(lock.PID, 0)
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ESRCH) {
		return true
	}
	// EPERM means the process exists but we cannot signal it (different
	// uid). Treat as alive — do NOT reclaim.
	return false
}

// ReleaseWorktreeLock deletes the lock file. Idempotent: a missing file
// is not an error.
func ReleaseWorktreeLock(worktreePath string) error {
	path := filepath.Join(worktreePath, worktreeEndlessDir, worktreeLockFile)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove worktree lock: %w", err)
	}
	return nil
}

// FindLockBySessionID scans <project_root>/.endless/worktrees/*/.endless/worktree.lock
// and returns the worktree path whose lock has the given session_id, or
// "" if none. SessionEnd uses this to release the lock without requiring
// the user to still be cwd'd inside the worktree.
//
// Returns "" with nil error if no match. Returns an error only on
// underlying I/O failures (the worktrees directory missing entirely is
// not an error — it just means no managed worktrees exist yet).
func FindLockBySessionID(projectID int64, sessionID string) (string, error) {
	if sessionID == "" {
		return "", nil
	}
	root, err := ProjectPath(projectID)
	if err != nil {
		return "", fmt.Errorf("project path: %w", err)
	}
	worktreesDir := filepath.Join(root, worktreeEndlessDir, worktreeWorktreesDir)
	entries, err := os.ReadDir(worktreesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read worktrees dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wtPath := filepath.Join(worktreesDir, e.Name())
		lock, err := ReadWorktreeLock(wtPath)
		if err != nil {
			// Skip worktrees with no lock or unreadable locks.
			continue
		}
		if lock.SessionID == sessionID {
			return wtPath, nil
		}
	}
	return "", nil
}

// FindWorktreeRoot walks up from cwd looking for .endless/worktree.json.
// Stops at projectRoot (inclusive — does not walk above it). Returns the
// directory containing .endless/worktree.json, or "" if no companion is
// found at or below projectRoot.
//
// projectRoot must be the absolute, resolved path to the registered
// project root; cwd is treated as absolute (Cleaned but not resolved
// for symlinks — callers pass the cwd as the hook received it).
func FindWorktreeRoot(cwd, projectRoot string) (string, error) {
	if cwd == "" || projectRoot == "" {
		return "", nil
	}
	dir := filepath.Clean(cwd)
	root := filepath.Clean(projectRoot)
	for {
		candidate := filepath.Join(dir, worktreeEndlessDir, worktreeCompanionFile)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return dir, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat %s: %w", candidate, err)
		}
		if dir == root {
			return "", nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding anything.
			return "", nil
		}
		dir = parent
	}
}
