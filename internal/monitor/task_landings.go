package monitor

import (
	"database/sql"
	"path/filepath"
	"strconv"
)

// E-1940 — the recorded landing is the only reliable "this work reached the
// base branch" signal.
//
// `worktree land` REBASES onto the base branch before fast-forwarding it, so
// every commit it lands is rewritten under a new SHA while the branch keeps the
// originals. SHA-identity probes therefore cannot see a landing at all:
//
//   - `git rev-list <base>..HEAD` counts the branch's pre-rebase commits
//     forever, and the count GROWS as the base advances. Measured on this
//     repo when the bug was found: 51 worktrees with a recorded landing still
//     reporting unlanded, 43 of them by 45-326 commits.
//   - `git branch --merged <base>` is false for exactly the branches it is
//     asked about, for the same reason.
//
// task_landings already records what actually happened. Crediting those commits
// is what turns "the SHAs differ" back into "the work is in".
//
// The credit is anchored on the BRANCH, not on the base: a landing SHA is
// excluded from the branch's own history, so the verdict survives a later
// rewrite of the base branch's history. That distinction is not academic — 80
// of this repo's 599 recorded landing SHAs are no longer reachable from main,
// while all 51 of the affected branches still reach their own landing SHA.

// LandedShasForTask returns the merge_commit_sha of every recorded landing for
// a task, newest first. An empty result is normal and means "never landed".
func LandedShasForTask(taskID int64) ([]string, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	return landedShas(db, taskID)
}

// landedShas is the query behind LandedShasForTask, taking the handle so the
// reaper (which already holds one) does not open a second.
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

// landedShasForWorktreePath is the best-effort lookup used by the path-based
// probe, which is handed a directory and nothing else. It derives the task id
// from the `e-NNNN` directory convention and reads the landings for it.
//
// Every failure yields nil, deliberately and without a fault. The path-based
// entry point stays usable where no database is reachable at all — inside a
// self-dev worktree the handle routes to a per-worktree sandbox that has no
// task row (E-1766), and a foreign directory has no task id to look up. Nil
// landings simply credit nothing, which returns the probe to its pre-E-1940
// answer: it may over-report unlanded work, and can never under-report it. That
// is the safe direction, and it is why this is silent where a failed git probe
// is not.
func landedShasForWorktreePath(worktreePath string) []string {
	m := worktreeDirRe.FindStringSubmatch(filepath.Base(worktreePath))
	if m == nil {
		return nil
	}
	taskID, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return nil
	}
	shas, err := LandedShasForTask(taskID)
	if err != nil {
		return nil
	}
	return shas
}
