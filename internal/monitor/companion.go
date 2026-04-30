package monitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CompanionFile is the per-session record written at SessionStart and
// removed at SessionEnd. It exposes a running AI coding session to other
// processes (most often a sibling tmux pane) without requiring DB access
// or process-tree introspection.
//
// Path: <project_root>/.endless/sessions/<harness>-<harness_session_id>.json
//
// The "<harness>-" filename prefix lets multiple harnesses (Claude, Codex,
// future others) coexist in one directory; readers filter by prefix.
type CompanionFile struct {
	Harness          string `json:"harness"`
	HarnessSessionID string `json:"harness_session_id"`
	EndlessSessionID int64  `json:"endless_session_id"`
	PaneID           string `json:"pane_id,omitempty"`
	CWD              string `json:"cwd"`
	PID              int    `json:"pid"`
	StartedAt        string `json:"started_at"`
	WorktreePath     string `json:"worktree_path,omitempty"`
}

const companionDirName = "sessions"

// CompanionDir returns the directory holding companion files for a project.
func CompanionDir(projectID int64) (string, error) {
	root, err := ProjectPath(projectID)
	if err != nil {
		return "", fmt.Errorf("project path: %w", err)
	}
	return companionDirAtRoot(root), nil
}

// CompanionPath returns the full path to the companion file for a given
// harness session within a project.
func CompanionPath(projectID int64, harness, harnessSessionID string) (string, error) {
	dir, err := CompanionDir(projectID)
	if err != nil {
		return "", err
	}
	return companionPathInDir(dir, harness, harnessSessionID), nil
}

// WriteCompanion writes the per-session companion file atomically. It
// overwrites any existing file for the same (harness, session_id) pair —
// a fresh SessionStart is authoritative over any stale leftover.
func WriteCompanion(projectID int64, c CompanionFile) error {
	root, err := ProjectPath(projectID)
	if err != nil {
		return fmt.Errorf("project path: %w", err)
	}
	return writeCompanionAtRoot(root, c)
}

// CompanionExists reports whether a companion file is on disk for the given
// harness session. A missing file returns (false, nil); other errors (e.g.
// permission denied) return (false, err).
func CompanionExists(projectID int64, harness, harnessSessionID string) (bool, error) {
	root, err := ProjectPath(projectID)
	if err != nil {
		return false, fmt.Errorf("project path: %w", err)
	}
	return companionExistsAtRoot(root, harness, harnessSessionID)
}

// WorktreePathForTask returns the absolute path to the worktree associated
// with the given task, or "" if no such worktree exists. Convention:
// <project_root>/.endless/worktrees/e-<task_id> or .../e-<task_id>-<slug>/.
// If multiple match, the lexicographically first directory wins.
func WorktreePathForTask(projectID int64, taskID int64) (string, error) {
	root, err := ProjectPath(projectID)
	if err != nil {
		return "", fmt.Errorf("project path: %w", err)
	}
	return worktreePathForTaskAtRoot(root, taskID)
}

// RemoveCompanion deletes the companion file for the given harness session.
// Idempotent: a missing file is not an error.
func RemoveCompanion(projectID int64, harness, harnessSessionID string) error {
	root, err := ProjectPath(projectID)
	if err != nil {
		return fmt.Errorf("project path: %w", err)
	}
	return removeCompanionAtRoot(root, harness, harnessSessionID)
}

// --- internals (operate on a project root path; no DB) -----------------

func companionDirAtRoot(projectRoot string) string {
	return filepath.Join(projectRoot, ".endless", companionDirName)
}

func companionPathInDir(dir, harness, harnessSessionID string) string {
	return filepath.Join(dir, fmt.Sprintf("%s-%s.json", harness, harnessSessionID))
}

func writeCompanionAtRoot(projectRoot string, c CompanionFile) error {
	if c.Harness == "" || c.HarnessSessionID == "" {
		return errors.New("companion: harness and harness_session_id are required")
	}
	if c.StartedAt == "" {
		c.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}

	dir := companionDirAtRoot(projectRoot)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create companion dir: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal companion: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".companion-*.tmp")
	if err != nil {
		return fmt.Errorf("create companion tempfile: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write companion: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close companion tempfile: %w", err)
	}

	target := companionPathInDir(dir, c.Harness, c.HarnessSessionID)
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("rename companion: %w", err)
	}
	return nil
}

func companionExistsAtRoot(projectRoot, harness, harnessSessionID string) (bool, error) {
	target := companionPathInDir(companionDirAtRoot(projectRoot), harness, harnessSessionID)
	if _, err := os.Stat(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat companion: %w", err)
	}
	return true, nil
}

func worktreePathForTaskAtRoot(projectRoot string, taskID int64) (string, error) {
	if taskID <= 0 {
		return "", nil
	}
	worktreesDir := filepath.Join(projectRoot, ".endless", "worktrees")
	prefix := fmt.Sprintf("e-%d", taskID)

	// Two patterns: bare 'e-<id>' and 'e-<id>-*'. Globs alone aren't strict
	// enough — 'e-1027*' also matches 'e-10270', so we filter post-glob.
	candidates := []string{filepath.Join(worktreesDir, prefix)}
	more, err := filepath.Glob(filepath.Join(worktreesDir, prefix+"-*"))
	if err != nil {
		return "", fmt.Errorf("glob worktrees: %w", err)
	}
	candidates = append(candidates, more...)
	sort.Strings(candidates)

	for _, c := range candidates {
		base := filepath.Base(c)
		if base != prefix && !strings.HasPrefix(base, prefix+"-") {
			continue
		}
		info, err := os.Stat(c)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return c, nil
		}
	}
	return "", nil
}

func removeCompanionAtRoot(projectRoot, harness, harnessSessionID string) error {
	target := companionPathInDir(companionDirAtRoot(projectRoot), harness, harnessSessionID)
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove companion: %w", err)
	}
	return nil
}
