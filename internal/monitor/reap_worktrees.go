package monitor

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/faults"
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

// ReapSandbox destroys the dev sandbox bound to a just-reaped worktree, named
// by its directory basename (e-NNNN). Nil means no reaper is wired and sandbox
// cleanup is skipped.
//
// A seam rather than a direct call because sandboxcmd imports this package, so
// the dependency cannot run the other way. cmd/endless-go wires it to
// sandboxcmd.ReapSandboxForWorktree at start-up; tests substitute a recorder.
//
// The implementation re-derives its own safety conditions — do NOT assume the
// worktree checks above cover the sandbox (E-1904).
var ReapSandbox func(worktreeName string) error

// ReapStaleWorktrees removes worktree directories that are clean and hold no
// commit the default branch cannot already reach, whose task has been untouched
// for longer than ttl, and whose directory no live process is sitting in.
//
// It used to require a row in task_landings, which read as "only reclaim what
// has landed". That gate turned out to be a proxy for the wrong thing: a
// worktree whose branch sits at the base with nothing to land is just as
// disposable as one that landed, and E-2087 found two of them (E-1360, E-1697)
// permanently unreclaimable because nothing had ever been recorded for them.
// The tempting shortcut — record a landing so they reap — writes a falsehood
// into the table the not-on-main report reads, so the gate moved instead: what
// is required now is that there be nothing to land, which a landing implies but
// does not exhaust. A directory with no recorded history at all is still
// skipped; see maybeReapWorktree.
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
		if !reaped {
			continue
		}
		log.Printf("reap worktrees: removed %s", displayPath(dir))
		reapBoundSandbox(e.Name())
	}
	return nil
}

// reapBoundSandbox destroys the sandbox bound to a reaped worktree, if a
// reaper is wired. Failures are logged, never propagated: the worktree is
// already gone, and a leftover sandbox must not make the sweep look failed.
func reapBoundSandbox(worktreeName string) {
	if ReapSandbox == nil {
		return
	}
	err := ReapSandbox(worktreeName)
	if err != nil {
		log.Printf("reap worktrees: %s: %v", worktreeName, err)
		return
	}
}

// maybeReapWorktree applies the per-directory decision logic and
// performs the reap when eligible. Returns (true, nil) when the dir
// was removed.
//
// Eligibility (all must hold):
//  1. The task has been touched at some recorded moment — a landing, or any
//     session activity. A directory with neither has no timestamp to measure a
//     TTL against, and the reaper cannot tell an abandoned worktree from one
//     created a minute ago, so it skips.
//  2. That moment — MAX(landed_at, session_tasks.updated_at) — is older than
//     cutoff. session_tasks.updated_at is upserted by every task.* event from a
//     session actor, so claim / status flip / decision / etc. all advance it
//     (see internal/events/session_tasks.go).
//  3. No active (sessionstate.Live) session has task_id pointing at
//     the task.
//  4. The worktree's working tree is clean, and every commit on its branch is
//     reachable from the project's default branch by SHA — crediting each
//     recorded landing, since a rebasing land rewrites those SHAs (E-1940).
//  5. No live process holds cwd inside the dir.
//
// Condition 4 is deliberately NOT the probe `session status` reads for its ◆
// and `task unsettled` explains. That probe answers by CONTENT and is exact;
// this one answers by SHA reachability and is merely sufficient — it can report
// work outstanding on a branch that holds none, never the reverse. The reasons
// are in reapNothingToLand, and both are load-bearing: this function's mistakes
// are asymmetric, and it runs on every tool call. Do not "unify" the two
// without reading that comment first — E-2087 did, and cost ~90s per sweep on
// PreToolUse and PostToolUse before it was reverted here.
//
// Every failure still means skip, never reap: a git error, an unparsable count,
// or a default branch that will not resolve all leave the directory alone. The
// reaper would rather keep a candidate it cannot reason about than destroy
// in-flight work.
//
// Conditions 3 and 5 are not evaluated here: they are WorktreeInUse, the one
// implementation `endless worktree drop` also consults (E-1947), and it is
// called after the cheap git conditions since its lsof probe is the expensive
// one. They are still listed above in significance order.
func maybeReapWorktree(db *sql.DB, projectRoot, dir string, taskID int64, cutoff time.Time) (bool, error) {
	// The most recent landing, if there is one. Its absence no longer
	// disqualifies the directory (E-2087) — it only means the branch name has
	// to come from git rather than from the row.
	var landedAt string
	// branch is nullable (E-1719): a historical/record-only landing records no
	// branch. Scan into NullString so a NULL row doesn't error, and fall back
	// to what git reports when it's absent.
	var branch sql.NullString
	err := db.QueryRow(
		`SELECT landed_at, branch
		 FROM task_landings
		 WHERE task_id = ?
		 ORDER BY landed_at DESC
		 LIMIT 1`,
		taskID,
	).Scan(&landedAt, &branch)
	hasLanding := true
	if err == sql.ErrNoRows {
		hasLanding = false
	} else if err != nil {
		return false, fmt.Errorf("query last landing: %w", err)
	}

	var latest time.Time
	if hasLanding {
		latest, err = time.Parse("2006-01-02T15:04:05", landedAt)
		if err != nil {
			return false, fmt.Errorf("parse landed_at %q: %w", landedAt, err)
		}
	}

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
	// Condition 1. Nothing was ever recorded about this task, so there is no
	// moment to age off. Skipping is the only safe answer: the alternative
	// reaps a directory created seconds ago whose session has not yet emitted
	// its first event.
	if latest.IsZero() {
		return false, nil
	}
	if latest.After(cutoff) {
		return false, nil
	}

	base, berr := DefaultBranch(dir)
	if berr != nil {
		recordReapDefaultBranchFault(dir, taskID, berr)
		return false, nil
	}

	// Condition 4, answered cheaply — see reapNothingToLand for why this is
	// deliberately NOT the probe behind ◆.
	landedRefs, lerr := landedShas(db, taskID)
	if lerr != nil {
		return false, fmt.Errorf("query landing shas: %w", lerr)
	}
	nothing, gerr := reapNothingToLand(dir, base, landedRefs)
	if gerr != nil || !nothing {
		return false, nil
	}

	// Condition 5 — a modified working tree.
	out, gerr := runGit(dir, "status", "--porcelain")
	if gerr != nil {
		return false, nil
	}
	if strings.TrimSpace(out) != "" {
		return false, nil
	}

	// Conditions 3 and 5 in one call — the shared "is anything still using
	// this directory" predicate `endless worktree drop` also consults, so the
	// two destructive paths can never disagree (E-1947). Placed here rather
	// than at condition 3's position in the list because its lsof probe is the
	// most expensive check in this function: the cheap local-git conditions
	// above have already excluded most candidates by now. Order among skip
	// reasons is not observable — every one of them yields (false, nil).
	inUse, _, err := WorktreeInUse(db, dir, taskID)
	if err != nil {
		return false, err
	}
	if inUse {
		return false, nil
	}

	// The branch to delete: the recorded one when there is a landing row, git's
	// answer when there is not (E-2087 stopped requiring a landing, so a
	// reapable worktree may have no row to read a name from). Read here rather
	// than with the conditions above so the extra git call is paid only by a
	// directory actually being removed.
	//
	// The git fallback is deliberately NOT applied to a landing row whose
	// branch is NULL: that is E-1719's record-only landing, where the absent
	// name is a recorded fact about what was landed, and this function has
	// always left such a branch alone.
	branchName := strings.TrimSpace(branch.String)
	if !hasLanding {
		if out, gerr := runGit(dir, "symbolic-ref", "--short", "--quiet", "HEAD"); gerr == nil {
			branchName = strings.TrimSpace(out)
		}
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
	// A record-only landing (E-1719) records no branch, and a detached HEAD has
	// none to read, so there may be nothing to delete — the dir removal above
	// is the whole reap in that case.
	if branchName != "" {
		if out, err := runGit(projectRoot, "branch", "-D", branchName); err != nil {
			// Branch deletion failure shouldn't unwind the dir removal —
			// log it but treat the reap as successful.
			log.Printf("reap worktrees: %s: git branch -D %s: %v: %s", displayPath(dir), branchName, err, out)
		}
	}
	return true, nil
}

// reapNothingToLand answers the reaper's condition 4 with a SUFFICIENT
// condition rather than an exact one: every commit on this branch is reachable
// from base by SHA, so there is provably nothing here to land.
//
// It is deliberately NOT the probe behind ◆ and `task unsettled`, and the
// asymmetry is the point. Those two surfaces answer a question a person is
// reading — "what is outstanding?" — where a wrong answer misleads, so they pay
// `git range-diff` to recognise a commit a rebasing land re-hashed (E-2087).
// This function answers a question that ends in `rm -rf`, where the two
// mistakes are not comparable: refusing to reap a reapable directory costs disk
// space, and reaping one that still holds work destroys it. A cheap containment
// test is wrong only in the safe direction — it never reports "nothing to land"
// about a branch that holds something.
//
// It is also on a hot path that has no business being slow. ReapWorktreesForProject
// is called from five hook branches in internal/hookcmd/claude.go, including
// PreToolUse and PostToolUse, so this runs before and after EVERY tool call in
// every session. E-2087 briefly routed it through the range-diff probe and made
// each sweep cost ~90 seconds, which did not degrade the product so much as stop
// it. Anything added here is paid per tool call; treat that as the constraint it
// is.
//
// The cost of being conservative: a branch whose work landed under rewritten
// SHAs reads as holding something, so its directory is never reclaimed and
// landed worktrees accumulate — the leak E-1940 and E-2087 each tried to close.
// That is a known, accepted debt, tracked for a proper fix (caching the exact
// verdict rather than recomputing it per tool call). A slow leak is survivable;
// a 90-second tool call is not.
//
// The recorded landings are still credited (E-1940): excluding each one from
// the range is one more `^sha` on the same single git call, and dropping it
// would cost the reaper a second fix it already had — a rebasing land rewrites
// every SHA, so without the credit a worktree that landed through `worktree
// land` could never be reclaimed at all. `--ignore-missing` covers a recorded
// SHA this clone no longer has; without it one absent object makes git exit 128
// and the candidate is skipped forever.
//
// A git error answers false: the reaper skips what it cannot inspect.
func reapNothingToLand(dir, base string, landed []string) (bool, error) {
	args := []string{"rev-list", "--count", "--ignore-missing", "HEAD", "^" + base}
	for _, sha := range landed {
		args = append(args, "^"+sha)
	}
	out, err := runGit(dir, args...)
	if err != nil {
		return false, err
	}
	n, perr := strconv.Atoi(strings.TrimSpace(out))
	if perr != nil {
		return false, fmt.Errorf("unparsable rev-list count %q", strings.TrimSpace(out))
	}
	return n == 0, nil
}

// landedShas returns the merge_commit_sha of every recorded landing for a task,
// newest first. Reaper-only: the display probe answers by content and needs no
// such credit (E-2087).
func landedShas(db *sql.DB, taskID int64) ([]string, error) {
	rows, err := db.Query(
		`SELECT merge_commit_sha
		   FROM task_landings
		  WHERE task_id = ? AND merge_commit_sha != ''
		  ORDER BY landed_at DESC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var shas []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		shas = append(shas, sha)
	}
	return shas, rows.Err()
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
//
// Runs `lsof -t -a -d cwd +D <dir>` and reads STDOUT, not the exit status.
// Both details are load-bearing, and getting either wrong silently disables
// the guard rather than failing visibly (E-1947 found this probe answering
// "no" for a directory a live process was demonstrably sitting in):
//
//   - `-a` ANDs the selection criteria. lsof ORs them by default, so
//     `-d cwd +D <dir>` means "fd is cwd OR path is under dir" — which
//     matches every process on the machine, since they all have a cwd.
//   - the exit status cannot decide anything. lsof exits 1 both when nothing
//     matched AND when its +D tree-walk hit an entry it could not stat, the
//     normal case on a real worktree. It returned 1 WITH matching output in
//     the reproduction. `-t` (terse) prints one PID per line and nothing at
//     all when there is no match, so stdout is an unambiguous answer.
//     internal/sandboxcmd/livewriters.go reached the same conclusion.
//
// A non-empty stdout therefore means live. An error is returned only when
// lsof could not be run at all (absent, not executable) — callers fail
// closed on that, so a machine without lsof refuses to remove rather than
// removing blind.
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
	cmd := exec.Command("lsof", "-t", "-a", "-d", "cwd", "+D", dir)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			// Never started: lsof missing, not executable, fork failure.
			return false, err
		}
	}
	return strings.TrimSpace(stdout.String()) != "", nil
}

// AnnotateSessionStatusUnsettled fills each row's Unsettled flag from the git
// state of its worktree, in place. Called only on the FLAT render path (E-1701)
// — the IDs-only --tree view does not surface the marker, so it skips the git
// cost. Best-effort: a row whose worktree is absent or whose git inspection
// errors is left Unsettled=false rather than failing the whole view.
func AnnotateSessionStatusUnsettled(rows []SessionStatusRow) {
	for i := range rows {
		rows[i].Unsettled = taskWorktreeUnsettled(rows[i].ProjectID, rows[i].ID)
	}
}

// taskWorktreeUnsettled reports the landed-vs-worktree delta for one task: true
// when its worktree exists AND diverges from main — either modified (an unclean
// working tree, i.e. uncommitted changes) or unlanded (commits on the branch not
// yet on main, or changes made since a land). This collapses "unlanded" and
// "modified since land" into the single ◆ the flat view renders (Mike,
// 2026-07-01): a clean worktree whose commits are all on main — the fully-landed
// steady state — is settled, and a task with no worktree has nothing to land.
//
// It reuses the exact git signals the reaper inverts to decide a worktree is
// safe to remove (reap_worktrees.go conditions 4 & 5), so the two surfaces agree
// on what "done and landed" means. Any git error is treated as settled: the
// view must never block or lie because a git call hiccuped.
//
// E-1865 collapsed this into a wrapper over TaskWorktreeUnsettledDetail so the
// ◆ marker and `task unsettled`'s explanation of it read the SAME probes; the
// git logic now lives in worktree_unsettled.go, and UnsettledDetail.Unsettled
// preserves this function's original short-circuit order exactly.
func taskWorktreeUnsettled(projectID, taskID int64) bool {
	return TaskWorktreeUnsettledDetail(projectID, taskID).Unsettled()
}

// recordReapDefaultBranchFault reports the resolver failure that makes a
// candidate permanently unreapable. Without it the sweep is silent about the
// condition — it just never reaps anything, forever, and the growing worktree
// directory is the only symptom (E-1940).
//
// Fingerprinted on the directory so a sweep over N unresolvable worktrees
// raises N incidents (one per task, which is what the operator needs to act on)
// rather than one per sweep per worktree.
func recordReapDefaultBranchFault(dir string, taskID int64, err error) {
	faults.Record(faults.Fault{
		Code:        faults.ErrCodeDefaultBranchUnresolved,
		Source:      "worktree:reap",
		Fingerprint: dir,
		Summary:     fmt.Sprintf("E-%d: no default branch, worktree cannot be reaped", taskID),
		Detail:      err.Error(),
		Fields: map[string]any{
			"task":     fmt.Sprintf("E-%d", taskID),
			"worktree": dir,
			"command":  "monitor.DefaultBranch",
			"error":    err.Error(),
		},
	})
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
	// The column is STORED form; everything below walks the filesystem (E-2011).
	path, err = ResolvedProjectPath(path)
	if err != nil {
		return fmt.Errorf("reap worktrees for project %d: resolve path: %w", projectID, err)
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
