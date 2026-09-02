package monitor

import (
	"database/sql"
	"fmt"
)

// InUseReason names which guard decided a worktree directory is still in use,
// in a form callers can print verbatim. Empty means "not in use".
type InUseReason string

const (
	// ReasonNone is the not-in-use answer.
	ReasonNone InUseReason = ""
	// ReasonActiveSession: a sessions row that has not ended still has this
	// task active. Catches a bound Claude session whose cwd has stepped OUT of
	// the worktree — invisible to the live-process probe, but the directory is
	// still that session's home and removing it orphans the session.
	ReasonActiveSession InUseReason = "a live session has this task active"
	// ReasonLiveProcess: some process holds cwd inside the directory right
	// now. Catches anything standing in it, Claude or not — invisible to the
	// sessions table.
	ReasonLiveProcess InUseReason = "a process is holding cwd inside the worktree"
	// ReasonUndetermined accompanies a non-nil error. Neither probe could be
	// completed, so the answer is the fail-closed one.
	ReasonUndetermined InUseReason = "could not determine whether the worktree is in use"
)

// WorktreeInUse reports whether anything still depends on a worktree directory,
// and what. It is the ONE implementation of that question: the reaper
// (maybeReapWorktree) and `endless worktree drop` (via the `endless-go worktree
// in-use` verb) both call it, so the two paths cannot drift. Do NOT reimplement
// either probe in a caller — including in Python, which is why the verb exists
// (E-1947).
//
// The two probes are complementary and BOTH are needed. The lsof probe misses a
// session that stepped out of the directory; the sessions probe misses a
// non-Claude process standing in it. Either alone leaves a hole through which a
// live session's cwd gets deleted.
//
// taskID <= 0 means the directory has no owning task (nothing to look up in
// sessions), so only the live-process probe runs. Callers that know the task —
// every endless-managed `e-NNN` worktree — must pass it.
//
// Fail closed: any error returns inUse=true alongside the error. This is the
// reaper's existing stance — it would rather skip a candidate it cannot reason
// about than destroy work — and it is the only safe default for a caller about
// to remove a directory.
func WorktreeInUse(db *sql.DB, dir string, taskID int64) (bool, InUseReason, error) {
	if taskID > 0 {
		var activeSessions int
		err := db.QueryRow(
			`SELECT count(*) FROM sessions WHERE task_id = ? AND state IN (`+liveSessionStates+`)`,
			taskID,
		).Scan(&activeSessions)
		if err != nil {
			return true, ReasonUndetermined, fmt.Errorf("query active sessions: %w", err)
		}
		if activeSessions > 0 {
			return true, ReasonActiveSession, nil
		}
	}

	live, err := hasLiveProcessInDir(dir)
	if err != nil {
		return true, ReasonUndetermined, fmt.Errorf("check live processes: %w", err)
	}
	if live {
		return true, ReasonLiveProcess, nil
	}

	return false, ReasonNone, nil
}
