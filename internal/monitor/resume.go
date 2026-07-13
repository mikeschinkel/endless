package monitor

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// ResumeTarget is the JSON contract for `session-query resume-target`. It
// carries everything `endless session resume` needs to relaunch a lost Claude
// session: the harness UUID to hand to `claude --resume`, and the task
// worktree to cd into first. SessionID is "" for a background-agent dispatch
// row that never started (no UUID yet); WorktreePath is "" when the task's
// worktree is not on disk. The Python caller turns either into a clear error.
type ResumeTarget struct {
	EndlessID    int64  `json:"endless_id"`
	SessionID    string `json:"session_id"`
	ActiveTaskID *int64 `json:"active_task_id"`
	WorktreePath string `json:"worktree_path"`
	State        string `json:"state"`
}

const resumeSelect = `SELECT id, session_id, COALESCE(project_id, 0), active_task_id, COALESCE(state, '')
	FROM sessions`

// ResolveResumeTarget resolves a task or session reference to the session
// `session resume` should relaunch. Resolution is task-first, because the
// primary handle is the task id shown on the tmux tab:
//
//   - "E-<n>" / "e-<n>": the task's most-recent resumable session (errors if none).
//   - bare "<n>":        the task's most-recent resumable session, else sessions.id <n>.
//   - anything else:     a Claude UUID (exact match, then unique prefix).
//
// "Resumable" means session_id IS NOT NULL. Unlike `session goto`, ended
// sessions are included: a tmux crash takes every session to a dead pane, and
// recovering exactly those is what resume is for.
func ResolveResumeTarget(ref string) (ResumeTarget, error) {
	db, err := DB()
	if err != nil {
		return ResumeTarget{}, err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ResumeTarget{}, fmt.Errorf("empty session/task reference")
	}

	bare := ref
	explicitTask := false
	if len(ref) > 2 && (ref[:2] == "E-" || ref[:2] == "e-") {
		bare, explicitTask = ref[2:], true
	}

	if n, convErr := strconv.ParseInt(bare, 10, 64); convErr == nil {
		t, found, err := resumeByTask(db, n)
		if err != nil {
			return ResumeTarget{}, err
		}
		if found {
			return t, nil
		}
		if explicitTask {
			return ResumeTarget{}, fmt.Errorf("no resumable Claude session found for task E-%d", n)
		}
		t, found, err = resumeBySessionID(db, n)
		if err != nil {
			return ResumeTarget{}, err
		}
		if found {
			return t, nil
		}
		return ResumeTarget{}, fmt.Errorf(
			"no task E-%d with a resumable session, and no session id %d", n, n)
	}

	return resumeByUUID(db, ref)
}

func resumeByTask(db *sql.DB, taskID int64) (ResumeTarget, bool, error) {
	row := db.QueryRow(resumeSelect+
		` WHERE active_task_id = ? AND session_id IS NOT NULL
		  ORDER BY last_activity DESC LIMIT 1`, taskID)
	return scanResume(row)
}

func resumeBySessionID(db *sql.DB, id int64) (ResumeTarget, bool, error) {
	return scanResume(db.QueryRow(resumeSelect+` WHERE id = ?`, id))
}

func resumeByUUID(db *sql.DB, ref string) (ResumeTarget, error) {
	t, found, err := scanResume(db.QueryRow(resumeSelect+` WHERE session_id = ?`, ref))
	if err != nil {
		return ResumeTarget{}, err
	}
	if found {
		return t, nil
	}

	// Collect the full result set BEFORE building any target: buildResumeTarget
	// issues its own query (ProjectPath), and doing that while this cursor is
	// open would deadlock on SQLite's single writer connection.
	rows, err := db.Query(resumeSelect+
		` WHERE session_id LIKE ? ORDER BY last_activity DESC`, ref+"%")
	if err != nil {
		return ResumeTarget{}, fmt.Errorf("query session prefix: %w", err)
	}
	var raws []resumeRow
	for rows.Next() {
		var r resumeRow
		if err := rows.Scan(&r.id, &r.sessionID, &r.projectID, &r.activeTask, &r.state); err != nil {
			rows.Close()
			return ResumeTarget{}, fmt.Errorf("scan session: %w", err)
		}
		raws = append(raws, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ResumeTarget{}, fmt.Errorf("iterate session prefix: %w", err)
	}
	rows.Close()

	switch len(raws) {
	case 0:
		return ResumeTarget{}, fmt.Errorf("no session matches %q", ref)
	case 1:
		return buildResumeTarget(raws[0])
	default:
		return ResumeTarget{}, fmt.Errorf(
			"ambiguous session prefix %q — %d matches; use more characters or the integer id",
			ref, len(raws))
	}
}

// resumeRow is the raw sessions scan shape; it is turned into a ResumeTarget by
// buildResumeTarget only after any cursor that produced it has been closed.
type resumeRow struct {
	id, projectID int64
	sessionID     sql.NullString
	activeTask    sql.NullInt64
	state         string
}

// scanResume adapts a single-row query; found is false on sql.ErrNoRows. The
// nested ProjectPath query in buildResumeTarget is safe here because QueryRow
// releases the connection once Scan returns.
func scanResume(row *sql.Row) (ResumeTarget, bool, error) {
	var r resumeRow
	err := row.Scan(&r.id, &r.sessionID, &r.projectID, &r.activeTask, &r.state)
	if err == sql.ErrNoRows {
		return ResumeTarget{}, false, nil
	}
	if err != nil {
		return ResumeTarget{}, false, fmt.Errorf("scan session: %w", err)
	}
	t, err := buildResumeTarget(r)
	return t, err == nil, err
}

func buildResumeTarget(r resumeRow) (ResumeTarget, error) {
	t := ResumeTarget{EndlessID: r.id, SessionID: r.sessionID.String, State: r.state}
	if r.activeTask.Valid {
		v := r.activeTask.Int64
		t.ActiveTaskID = &v
		wt, err := WorktreePathForTask(r.projectID, v)
		if err != nil {
			return ResumeTarget{}, err
		}
		t.WorktreePath = wt
	}
	return t, nil
}
