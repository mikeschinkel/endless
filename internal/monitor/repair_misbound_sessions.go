package monitor

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Repairing the rows the reused-spawn-window bug already created (E-1983).
//
// Before the cwd-only bind rule, a tmux window that outlived its spawned session
// handed its @endless_task_id to whatever Claude session started in it next. The
// result is tasks carrying several `sessions` rows, where some of those rows are
// genuine (one Endless session's successive instances) and some are unrelated
// sessions that were bound by accident.
//
// The discriminator is the LAUNCH DIRECTORY: the working_dir the FIRST hook event
// for a session recorded. It is the only durable record of where a `claude` was
// started, and under the new rule it is also the only thing that may bind. A row
// launched inside task T's own worktree belongs to T; a row launched somewhere
// else and bound to T was bound by the window, not by the directory.
//
// It has to be the FIRST working_dir, never the last. A `/cd` changes a
// session's per-line cwd without moving the session — a trap that already made
// one reading of this data mistake two separate sessions for one session's
// clears.
type sessionLaunchRow struct {
	RowID        int64
	SessionID    string
	State        string
	LastActivity string
	LaunchDir    string // "" when no hook event ever recorded one
	InOwnTree    bool
}

// The verdicts RepairMisboundSessions reaches, one per task. Nothing is folded
// in silently: every task in the population gets a verdict and a reason, and the
// caller prints them.
const (
	// RepairVerdictRepaired — at least one row launched in the task's own
	// worktree, so those rows are the task's and every other row was mis-bound.
	RepairVerdictRepaired = "repaired"
	// RepairVerdictInstances — every row shares one launch directory, so they
	// are one session's successive instances (a /clear mints a new harness id).
	// Not this bug; E-2063 owns whether they collapse.
	RepairVerdictInstances = "instances"
	// RepairVerdictUndecided — the rows disagree about where they launched and
	// none launched in the task's own worktree, so there is no evidence for which
	// one is genuine. Left exactly as found, because unbinding on a guess can
	// destroy the only binding a task has.
	RepairVerdictUndecided = "undecided"
)

// liveSessionWindow is how recently a row must have been active to be treated as
// LIVE and left alone. A repair that unbinds a session someone is sitting in
// would take that session's task away mid-turn, and under write-once task_id it
// could not simply be put back. Generous on purpose: every row this repair is
// actually aimed at has been idle for weeks.
const liveSessionWindow = 6 * time.Hour

// TaskRepair is what RepairMisboundSessions decided for one task.
type TaskRepair struct {
	TaskID  int64
	Verdict string
	Reason  string
	Kept    []int64 // sessions.id rows left bound
	Unbound []int64 // sessions.id rows whose task_id this repair NULLed
}

// String renders one task's verdict as the repair's caller prints it.
func (r TaskRepair) String() string {
	s := fmt.Sprintf("E-%d %s: %s", r.TaskID, r.Verdict, r.Reason)
	if len(r.Unbound) > 0 {
		s += fmt.Sprintf(" [unbound session rows %s; kept %s]",
			joinIDs(r.Unbound), joinIDs(r.Kept))
	}
	return s
}

func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%d", id)
	}
	return strings.Join(parts, ",")
}

// misboundQuery collects every sessions row belonging to a task that has more
// than one, joined to the working_dir its session's FIRST hook event recorded.
//
// activity.session_context is JSON carrying the harness session id, which is
// what sessions.session_id holds — there is no foreign key between the two
// tables, so the join goes through that extraction.
const misboundQuery = `
WITH launch AS (
  SELECT uuid, working_dir FROM (
    SELECT json_extract(session_context, '$.session_id') AS uuid,
           working_dir,
           ROW_NUMBER() OVER (
             PARTITION BY json_extract(session_context, '$.session_id')
             ORDER BY id
           ) AS rn
    FROM activity
    WHERE session_context IS NOT NULL
  ) WHERE rn = 1
),
multi AS (
  SELECT task_id FROM sessions
  WHERE task_id IS NOT NULL
  GROUP BY task_id HAVING count(*) > 1
)
SELECT s.task_id, s.id, s.session_id, s.state,
       COALESCE(s.last_activity, ''), COALESCE(l.working_dir, '')
FROM sessions s
JOIN multi m ON m.task_id = s.task_id
LEFT JOIN launch l ON l.uuid = s.session_id
ORDER BY s.task_id, s.id`

// RepairMisboundSessions unbinds the sessions rows this bug mis-bound and
// returns what it decided for every task it looked at, in task order. The caller
// prints them; nothing here logs.
//
// The write-once trigger on sessions.task_id aborts a non-NULL -> NULL write, so
// the caller must have dropped it for the duration of the transaction and must
// re-create it before committing. That is the drop-repair-recreate shape
// E-1969's change file already uses, and it keeps write-once absolute for every
// runtime caller — no verb gains a bypass.
//
// Idempotent: a second run finds the mis-bound rows already NULL, so they are no
// longer part of any task's row set and nothing changes.
func RepairMisboundSessions(tx *sql.Tx) ([]TaskRepair, error) {
	byTask, order, err := loadMisboundRows(tx)
	if err != nil {
		return nil, err
	}
	repairs := make([]TaskRepair, 0, len(order))
	for _, taskID := range order {
		repair := decideTaskRepair(taskID, byTask[taskID])
		for _, rowID := range repair.Unbound {
			if _, err = tx.Exec(
				"UPDATE sessions SET task_id = NULL WHERE id = ?", rowID,
			); err != nil {
				return nil, fmt.Errorf("unbinding session row %d from task %d: %w",
					rowID, taskID, err)
			}
		}
		repairs = append(repairs, repair)
	}
	return repairs, nil
}

// loadMisboundRows runs misboundQuery and groups its rows by task, returning the
// groups and the task ids in ascending order.
func loadMisboundRows(tx *sql.Tx) (map[int64][]sessionLaunchRow, []int64, error) {
	rows, err := tx.Query(misboundQuery)
	if err != nil {
		return nil, nil, fmt.Errorf("selecting multi-session tasks: %w", err)
	}
	defer rows.Close()

	byTask := map[int64][]sessionLaunchRow{}
	for rows.Next() {
		var taskID int64
		var r sessionLaunchRow
		if err = rows.Scan(&taskID, &r.RowID, &r.SessionID, &r.State,
			&r.LastActivity, &r.LaunchDir); err != nil {
			return nil, nil, fmt.Errorf("scanning session row: %w", err)
		}
		r.InOwnTree = launchedInTaskWorktree(r.LaunchDir, taskID)
		byTask[taskID] = append(byTask[taskID], r)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading session rows: %w", err)
	}

	order := make([]int64, 0, len(byTask))
	for taskID := range byTask {
		order = append(order, taskID)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	return byTask, order, nil
}

// launchedInTaskWorktree reports whether launchDir is task taskID's own worktree
// or a directory inside it. Uses TaskIDFromWorktreePath, the same path-convention
// authority the bind itself reads (E-1301), so the repair and the live rule agree
// on what "this task's worktree" means.
func launchedInTaskWorktree(launchDir string, taskID int64) bool {
	if launchDir == "" {
		return false
	}
	return TaskIDFromWorktreePath(launchDir) == fmt.Sprintf("E-%d", taskID)
}

// decideTaskRepair applies the three verdicts to one task's rows. Pure — no DB
// access — so the judgment is unit-testable without fixtures.
func decideTaskRepair(taskID int64, rows []sessionLaunchRow) TaskRepair {
	repair := TaskRepair{TaskID: taskID}

	var own, foreign []sessionLaunchRow
	for _, r := range rows {
		if r.InOwnTree {
			own = append(own, r)
			continue
		}
		foreign = append(foreign, r)
	}

	if len(own) == 0 {
		return undecidedOrInstances(repair, rows)
	}

	repair.Verdict = RepairVerdictRepaired
	for _, r := range own {
		repair.Kept = append(repair.Kept, r.RowID)
	}
	var held []string
	for _, r := range foreign {
		// A row that was active in the last few hours is somebody's live session.
		// Leave it and say so rather than taking its task away mid-turn.
		if sessionIsLive(r.LastActivity) {
			repair.Kept = append(repair.Kept, r.RowID)
			held = append(held, fmt.Sprintf("%d (state=%s, active %s)",
				r.RowID, r.State, r.LastActivity))
			continue
		}
		repair.Unbound = append(repair.Unbound, r.RowID)
	}
	repair.Reason = fmt.Sprintf(
		"%d row(s) launched in this task's worktree, %d launched elsewhere",
		len(own), len(foreign))
	if len(held) > 0 {
		repair.Reason += fmt.Sprintf(
			"; left %s bound because it is still live", strings.Join(held, ", "))
	}
	return repair
}

// undecidedOrInstances handles a task no row of which launched in its own
// worktree: one shared launch directory means successive instances of one
// session, anything else means the evidence does not say which row is genuine.
func undecidedOrInstances(repair TaskRepair, rows []sessionLaunchRow) TaskRepair {
	dirs := map[string]struct{}{}
	for _, r := range rows {
		dirs[r.LaunchDir] = struct{}{}
		repair.Kept = append(repair.Kept, r.RowID)
	}
	if len(dirs) == 1 {
		var only string
		for d := range dirs {
			only = d
		}
		if only == "" {
			repair.Verdict = RepairVerdictUndecided
			repair.Reason = "no launch directory was ever recorded for any row"
			return repair
		}
		repair.Verdict = RepairVerdictInstances
		repair.Reason = fmt.Sprintf(
			"all %d rows launched in %s — one session's instances, not a mis-bind",
			len(rows), only)
		return repair
	}
	repair.Verdict = RepairVerdictUndecided
	repair.Reason = fmt.Sprintf(
		"%d rows across %d launch directories and none in this task's worktree — "+
			"no evidence says which is genuine, so nothing was changed",
		len(rows), len(dirs))
	return repair
}

// sessionIsLive reports whether last_activity is recent enough that a human may
// be sitting in this session right now. An unparseable or empty timestamp is NOT
// live: every such row predates the columns being written reliably.
func sessionIsLive(lastActivity string) bool {
	if lastActivity == "" {
		return false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if ts, err := time.Parse(layout, lastActivity); err == nil {
			return time.Since(ts) < liveSessionWindow
		}
	}
	return false
}
