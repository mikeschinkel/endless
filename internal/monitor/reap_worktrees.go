package monitor

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultWorktreeTTL is the fallback grace period applied when a project's
// .endless/config.json does not specify worktree_ttl.
const DefaultWorktreeTTL = 14 * 24 * time.Hour

// dayDurationRe matches a duration prefix of the form "<digits>d".
// time.ParseDuration does not understand "d"; ParseWorktreeTTL strips
// any leading day component and converts it to hours before delegating.
var dayDurationRe = regexp.MustCompile(`^(\d+)d`)

// ParseWorktreeTTL parses a duration string with optional day component.
// Accepts: "14d", "24h", "30m", "3600s", combinations like "7d12h".
// Empty or whitespace-only input is rejected; callers fall back to
// DefaultWorktreeTTL when no config field is set.
func ParseWorktreeTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("worktree ttl: empty")
	}
	var days time.Duration
	if m := dayDurationRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, fmt.Errorf("worktree ttl: parse days %q: %w", m[1], err)
		}
		days = time.Duration(n) * 24 * time.Hour
		s = s[len(m[0]):]
	}
	if s == "" {
		return days, nil
	}
	rest, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("worktree ttl: parse %q: %w", s, err)
	}
	return days + rest, nil
}

// worktreeDirRe matches the directory naming convention
// .endless/worktrees/e-NNNN — only the integer portion is the task ID.
var worktreeDirRe = regexp.MustCompile(`^e-(\d+)$`)

// ReapStaleWorktrees removes worktree directories whose owning task has
// at least one row in task_landings older than ttl AND has no live
// process holding cwd inside the directory.
//
// Pre-existing orphan directories (no rows in task_landings) are
// skipped — the reaper only touches dirs whose task has landed at
// least once.
//
// projectRoot is the main checkout path; `git worktree remove` runs
// there. The function is idempotent and best-effort: per-directory
// failures are logged and do not abort the sweep.
func ReapStaleWorktrees(projectRoot string, ttl time.Duration) error {
	worktreeRoot := filepath.Join(projectRoot, ".endless", "worktrees")
	entries, err := os.ReadDir(worktreeRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reap worktrees: read %s: %w", worktreeRoot, err)
	}

	db, err := DB()
	if err != nil {
		return fmt.Errorf("reap worktrees: db: %w", err)
	}

	cutoff := time.Now().UTC().Add(-ttl)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := worktreeDirRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		taskID, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}
		dir := filepath.Join(worktreeRoot, e.Name())
		reaped, err := maybeReapWorktree(db, projectRoot, dir, taskID, cutoff)
		if err != nil {
			log.Printf("reap worktrees: %s: %v", dir, err)
			continue
		}
		if reaped {
			log.Printf("reap worktrees: removed %s", dir)
		}
	}
	return nil
}

// maybeReapWorktree applies the per-directory decision logic and
// performs the reap when eligible. Returns (true, nil) when the dir
// was removed.
func maybeReapWorktree(db *sql.DB, projectRoot, dir string, taskID int64, cutoff time.Time) (bool, error) {
	var landedAt, branch string
	err := db.QueryRow(
		`SELECT landed_at, branch
		 FROM task_landings
		 WHERE task_id = ?
		 ORDER BY landed_at DESC
		 LIMIT 1`,
		taskID,
	).Scan(&landedAt, &branch)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query last landing: %w", err)
	}

	landed, err := time.Parse("2006-01-02T15:04:05", landedAt)
	if err != nil {
		return false, fmt.Errorf("parse landed_at %q: %w", landedAt, err)
	}
	if landed.After(cutoff) {
		return false, nil
	}

	live, err := hasLiveProcessInDir(dir)
	if err != nil {
		return false, fmt.Errorf("check live processes: %w", err)
	}
	if live {
		return false, nil
	}

	if out, err := runGit(projectRoot, "worktree", "remove", "--force", dir); err != nil {
		return false, fmt.Errorf("git worktree remove: %v: %s", err, out)
	}
	if out, err := runGit(projectRoot, "branch", "-D", branch); err != nil {
		// Branch deletion failure shouldn't unwind the dir removal —
		// log it but treat the reap as successful.
		log.Printf("reap worktrees: %s: git branch -D %s: %v: %s", dir, branch, err, out)
	}
	return true, nil
}

// hasLiveProcessInDir reports whether any process has cwd inside dir.
// Uses `lsof -d cwd +D <dir>`:
//   - exit 0 with output → at least one match → live
//   - exit 1 with empty output → no matches → not live (the common case
//     post-TTL on an abandoned worktree)
//   - other exit codes → propagate as an error so the caller skips reap
//     rather than guessing
//
// stderr is intentionally discarded: lsof emits "can't stat() smbfs file
// system /Volumes/.timemachine/..." warnings on macOS with mounted
// Time Machine drives. Those warnings are about unrelated filesystems
// and have no bearing on whether the worktree directory is in use.
func hasLiveProcessInDir(dir string) (bool, error) {
	cmd := exec.Command("lsof", "-d", "cwd", "+D", dir)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	err := cmd.Run()
	if err == nil {
		return stdout.Len() > 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == 1 {
			return false, nil
		}
	}
	return false, err
}

// runGit executes `git -C <projectRoot> <args...>` and returns the
// combined stdout+stderr along with the error.
func runGit(projectRoot string, args ...string) (string, error) {
	full := append([]string{"-C", projectRoot}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ReadWorktreeTTLConfig reads the worktree_ttl string field from
// <projectRoot>/.endless/config.json. Returns "" when the file is
// absent, unreadable, or has no such field — callers fall back to
// DefaultWorktreeTTL.
func ReadWorktreeTTLConfig(projectRoot string) string {
	path := filepath.Join(projectRoot, ".endless", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg struct {
		WorktreeTTL string `json:"worktree_ttl"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.WorktreeTTL
}

// ReapWorktreesForProject resolves the project's filesystem path and
// configured TTL, then runs ReapStaleWorktrees. Used by callers (e.g.
// the endless-hook event handlers) that have a projectID but not the
// path. Returns silently when projectID has no row or no path.
func ReapWorktreesForProject(projectID int64) error {
	db, err := DB()
	if err != nil {
		return fmt.Errorf("reap worktrees for project %d: db: %w", projectID, err)
	}
	var path string
	err = db.QueryRow("SELECT path FROM projects WHERE id = ?", projectID).Scan(&path)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reap worktrees for project %d: lookup path: %w", projectID, err)
	}
	if path == "" {
		return nil
	}
	ttl := DefaultWorktreeTTL
	if s := ReadWorktreeTTLConfig(path); s != "" {
		if parsed, perr := ParseWorktreeTTL(s); perr == nil {
			ttl = parsed
		} else {
			log.Printf("reap worktrees for project %d: parse ttl %q: %v (using default %s)",
				projectID, s, perr, DefaultWorktreeTTL)
		}
	}
	return ReapStaleWorktrees(path, ttl)
}
