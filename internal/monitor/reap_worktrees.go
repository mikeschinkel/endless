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
			log.Printf("reap worktrees: %s: %v", displayPath(dir), err)
			continue
		}
		if reaped {
			log.Printf("reap worktrees: removed %s", displayPath(dir))
		}
	}
	return nil
}

// maybeReapWorktree applies the per-directory decision logic and
// performs the reap when eligible. Returns (true, nil) when the dir
// was removed.
//
// Eligibility (all must hold):
//  1. A task_landings row exists for the task.
//  2. The latest activity timestamp — MAX(landed_at, session_tasks.updated_at)
//     — is older than cutoff. session_tasks.updated_at is upserted by every
//     task.* event from a session actor, so claim / status flip / decision /
//     etc. all advance it (see internal/events/session_tasks.go).
//  3. No active (state != 'ended') session has active_task_id pointing at
//     the task.
//  4. The worktree's branch has no commits not yet on main
//     (`git -C <wt> rev-list main..HEAD --count` == 0).
//  5. The worktree's working tree is clean
//     (`git -C <wt> status --porcelain` empty).
//  6. No live process holds cwd inside the dir.
//
// Any git error while running 4 or 5 is treated as "in use" — the
// reaper would rather skip a candidate it can't reason about than
// destroy in-flight work.
func maybeReapWorktree(db *sql.DB, projectRoot, dir string, taskID int64, cutoff time.Time) (bool, error) {
	var landedAt string
	// branch is nullable (E-1719): a historical/record-only landing records no
	// branch. Scan into NullString so a NULL row doesn't error, and skip the
	// branch -D step below when it's absent.
	var branch sql.NullString
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
	latest := landed

	var sessionTouched sql.NullString
	err = db.QueryRow(
		`SELECT MAX(updated_at) FROM session_tasks WHERE task_id = ?`,
		taskID,
	).Scan(&sessionTouched)
	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("query session_tasks: %w", err)
	}
	if sessionTouched.Valid && sessionTouched.String != "" {
		if t, perr := time.Parse("2006-01-02T15:04:05", sessionTouched.String); perr == nil && t.After(latest) {
			latest = t
		}
	}
	if latest.After(cutoff) {
		return false, nil
	}

	var activeSessions int
	err = db.QueryRow(
		`SELECT count(*) FROM sessions WHERE active_task_id = ? AND state != 'ended'`,
		taskID,
	).Scan(&activeSessions)
	if err != nil {
		return false, fmt.Errorf("query active sessions: %w", err)
	}
	if activeSessions > 0 {
		return false, nil
	}

	// Unmerged commits on the branch → skip. Hardcoded `main` matches
	// the endless convention (consistent with `just land`). Treat any
	// git error as "in use" — better to skip than destroy a worktree
	// we can't inspect.
	out, gerr := runGit(dir, "rev-list", "main..HEAD", "--count")
	if gerr != nil {
		return false, nil
	}
	if n, perr := strconv.Atoi(strings.TrimSpace(out)); perr != nil || n > 0 {
		return false, nil
	}

	// Dirty working tree → skip.
	out, gerr = runGit(dir, "status", "--porcelain")
	if gerr != nil {
		return false, nil
	}
	if strings.TrimSpace(out) != "" {
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
		// A stranded leftover: git's worktree admin no longer knows the
		// path (e.g. a prior reap removed the record but didn't rmdir),
		// so `git worktree remove` aborts with "is not a working tree".
		// Remove the whole dir, strictly path-scoped so RemoveAll can
		// never over-reach. Skip branch -D (we can't reason about the
		// branch from a dir git doesn't track). A genuine failure
		// (permissions, etc.) propagates and is surfaced by the outer
		// sweep's existing log line — no more false "removed".
		if strings.Contains(out, "is not a working tree") {
			if rerr := removeStrandedWorktreeDir(projectRoot, dir); rerr != nil {
				return false, fmt.Errorf("remove stranded worktree dir: %w", rerr)
			}
			return true, nil
		}
		return false, fmt.Errorf("git worktree remove: %v: %s", err, out)
	}
	// A record-only landing (E-1719) records no branch, so there is nothing to
	// delete — the dir removal above is the whole reap in that case.
	if branch.Valid && branch.String != "" {
		if out, err := runGit(projectRoot, "branch", "-D", branch.String); err != nil {
			// Branch deletion failure shouldn't unwind the dir removal —
			// log it but treat the reap as successful.
			log.Printf("reap worktrees: %s: git branch -D %s: %v: %s", displayPath(dir), branch.String, err, out)
		}
	}
	return true, nil
}

// removeStrandedWorktreeDir deletes a stranded orphan worktree directory
// (one git no longer tracks) with os.RemoveAll, but ONLY after asserting the
// path is exactly a <projectRoot>/.endless/worktrees/e-NNN directory that is
// not a symlink. Any assertion failing returns an error and deletes nothing,
// so RemoveAll can never over-reach to a path we can't vouch for or follow a
// symlink pointing elsewhere.
func removeStrandedWorktreeDir(projectRoot, dir string) error {
	worktreeRoot := filepath.Join(projectRoot, ".endless", "worktrees")
	rel, err := filepath.Rel(worktreeRoot, dir)
	if err != nil {
		return fmt.Errorf("resolve %s under worktrees root: %w", dir, err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refuse to remove %s: outside worktrees root %s", dir, worktreeRoot)
	}
	if !worktreeDirRe.MatchString(filepath.Base(dir)) {
		return fmt.Errorf("refuse to remove %s: basename is not an e-NNN worktree dir", dir)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("lstat %s: %w", dir, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to remove %s: is a symlink", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove all %s: %w", dir, err)
	}
	return nil
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
//
// Held in a var (paired with runGit above) so reaper tests can swap
// in a deterministic stub.
var hasLiveProcessInDir = realHasLiveProcessInDir

func realHasLiveProcessInDir(dir string) (bool, error) {
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

// AnnotateSessionStatusDirty fills each row's Dirty flag from the git state of
// its worktree, in place. Called only on the FLAT render path (E-1701) — the
// IDs-only --tree view does not surface the marker, so it skips the git cost.
// Best-effort: a row whose worktree is absent or whose git inspection errors is
// left Dirty=false rather than failing the whole view.
func AnnotateSessionStatusDirty(rows []SessionStatusRow) {
	for i := range rows {
		rows[i].Dirty = taskWorktreeDirty(rows[i].ProjectID, rows[i].ID)
	}
}

// taskWorktreeDirty reports the landed-vs-worktree delta for one task: true when
// its worktree exists AND diverges from main — either an unclean working tree
// (uncommitted changes) or commits on the branch not yet on main (unlanded, or
// changes made since a land). This collapses "not landed" and "dirty since land"
// into the single ◆ the flat view renders (Mike, 2026-07-01): a clean worktree
// whose commits are all on main — the fully-landed steady state — is not dirty,
// and a task with no worktree has nothing to land.
//
// It reuses the exact git signals the reaper inverts to decide a worktree is
// safe to remove (reap_worktrees.go conditions 4 & 5), so the two surfaces agree
// on what "done and landed" means. Any git error is treated as not-dirty: the
// view must never block or lie because a git call hiccuped.
func taskWorktreeDirty(projectID, taskID int64) bool {
	wt, err := WorktreePathForTask(projectID, taskID)
	if err != nil || wt == "" {
		return false
	}
	// Uncommitted changes → dirty. A git error here means we can't reason about
	// the tree, so fall through to not-dirty rather than guess.
	out, gerr := runGit(wt, "status", "--porcelain")
	if gerr != nil {
		return false
	}
	if strings.TrimSpace(out) != "" {
		return true
	}
	// Commits on the branch not yet on main → unlanded work.
	out, gerr = runGit(wt, "rev-list", "main..HEAD", "--count")
	if gerr != nil {
		return false
	}
	n, perr := strconv.Atoi(strings.TrimSpace(out))
	return perr == nil && n > 0
}

// runGit executes `git -C <dir> <args...>` and returns the combined
// stdout+stderr along with the error. dir is the directory passed to
// `git -C`; callers pass projectRoot for repo-level operations
// (`worktree remove`, `branch -D`) and the worktree dir itself for
// per-worktree inspection (`rev-list`, `status`).
//
// Held as a var rather than a plain func so reaper tests can substitute
// a fixture-driven implementation without building real git fixtures.
// Restore the original via the returned func from SetRunGitForTest.
var runGit = func(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// displayPath returns a human-friendly form of path for log output:
//  1. cwd-relative (no "./" prefix) when path is a descendant of cwd
//  2. "~/..." when path is a descendant of $HOME
//  3. unchanged otherwise
//
// Best-effort: an os.Getwd / os.UserHomeDir error falls through to the
// next branch. Avoids "../"-prefixed results (those are uglier than
// the alternative). Held as a var so reaper tests can pin cwd
// behavior without manipulating the real process cwd.
var displayPath = func(path string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, path); err == nil &&
			rel != "." && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, path); err == nil &&
			rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return path
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
