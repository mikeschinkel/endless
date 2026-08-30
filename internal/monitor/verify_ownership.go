package monitor

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// SuiteOwnership is the answer to the only two questions the verify runner's
// own-task-only refusal is built from: has the requested task LANDED, and is it
// one of the tasks this caller may verify?
//
// Both come from the MAIN database, deliberately and unconditionally — see
// suiteOwnershipDB. Neither question has a meaningful answer anywhere else: a
// per-worktree sandbox has no landings and no sessions, so asking it would
// return "not landed, no session" for every task and quietly disable the
// refusal in exactly the self-dev case that produced the incidents behind
// E-2023.
type SuiteOwnership struct {
	// Known is false when the main database could not be read at all — a fresh
	// install before its first command, or a suite running under the verify
	// runner's own isolated HOME. The caller must fail OPEN on it: refusing on
	// an unanswerable question would make a task unverifiable on the machine
	// where its suite was written.
	Known bool

	// Landed is true when the task has at least one task_landings row. It is
	// the same bool `session-query task-report` reports, read from the same
	// table, so the runner refusal and the PreToolUse hook (E-1916) cannot
	// arrive at different notions of landed-ness.
	Landed bool

	// Owned is true when the requested task is one of Tasks.
	Owned bool

	// Tasks are the task ids this caller may verify, in resolution order. It is
	// a SET rather than a single "active task" because the caller's identity has
	// more than one honest source and each is legitimate on its own: the
	// session named by ENDLESS_SESSION_ID (exported by `esu`) and the task whose
	// worktree the runner is standing in. They agree in the sanctioned case; a
	// stale export disagreeing with the checkout is a reason to allow, not to
	// refuse work someone is plainly doing in its own worktree.
	Tasks []int64

	// Source labels where each entry in Tasks came from, index-aligned, so the
	// refusal can show its work instead of asserting a conclusion.
	Source []string
}

// SuiteOwnershipFor answers whether taskID's verification suite may be run from
// root (the checkout the runner resolved). It never fails the caller: a
// database that cannot be read returns Known=false and a nil error, because the
// refusal this feeds is a guard against a mistake, not a precondition for
// working.
func SuiteOwnershipFor(taskID int64, root string) (o SuiteOwnership, err error) {
	db, err := suiteOwnershipDB()
	if err != nil || db == nil {
		return o, err
	}
	defer db.Close()

	o.Known = true
	o.Landed, err = taskHasLanded(db, taskID)
	if err != nil {
		return SuiteOwnership{}, err
	}
	o.Tasks, o.Source = callerTasks(db, root)
	for _, id := range o.Tasks {
		if id == taskID {
			o.Owned = true
			break
		}
	}
	return o, nil
}

// suiteOwnershipDB opens the deployed installation's database directly,
// read-only, bypassing every routing decision this process made.
//
// It does NOT go through DB(). DB() applies schema and runs the enum integrity
// gates, which a candidate build must never do to a database it does not own
// (E-1818), and it follows --config-dir / the self-detected sandbox, which is
// the routing this lookup has to ignore. A plain open of realDBPath() reads the
// one database where landings and sessions actually live, whichever database
// the rest of the command is talking to.
//
// A missing file returns (nil, nil): sql.Open would CREATE it, and a runner that
// leaves an empty database behind as a side effect of a guard is worse than an
// unanswered guard.
func suiteOwnershipDB() (*sql.DB, error) {
	path := realDBPath()
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// Match the main connection's busy_timeout so a concurrent writer makes this
	// read wait rather than fail — a spurious SQLITE_BUSY here would surface as a
	// refusal, which is the one outcome a transient lock must not cause.
	if _, err = db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure %s: %w", path, err)
	}
	return db, nil
}

// taskHasLanded reports whether the task has any task_landings row. A database
// too old to have the table is treated as "not landed" rather than an error:
// nothing has landed in a schema that cannot record a landing.
func taskHasLanded(db *sql.DB, taskID int64) (bool, error) {
	var landed bool
	err := db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM task_landings WHERE task_id = ?)", taskID,
	).Scan(&landed)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return false, nil
		}
		return false, fmt.Errorf("read landed for E-%d: %w", taskID, err)
	}
	return landed, nil
}

// callerTasks resolves the tasks this caller may verify, in order:
//
//  1. ENDLESS_SESSION_ID — the session id `esu` / `endless shell-init` exports
//     into the user's own shell. Its session's task is the caller's task.
//  2. The checkout itself. A task worktree's path names its task, so a runner
//     standing in .endless/worktrees/e-NNNN is verifying E-NNNN by construction.
//
// The second is the one that matters most and the one that cannot be faked by
// an inherited environment: an agent's shell carries no ENDLESS_SESSION_ID, and
// the incident this guard exists to stop (a glob over the suite directory) ran
// from a worktree whose path named a different task than every suite it
// executed.
//
// Errors are swallowed on purpose. An unresolvable session is not an error
// condition — it is the ordinary state of a bare terminal — and the caller's
// fail-open contract is expressed by an empty result, not by a returned error.
func callerTasks(db *sql.DB, root string) (tasks []int64, source []string) {
	add := func(id int64, src string) {
		if id <= 0 {
			return
		}
		for _, have := range tasks {
			if have == id {
				return
			}
		}
		tasks = append(tasks, id)
		source = append(source, src)
	}

	if sid, err := strconv.ParseInt(os.Getenv("ENDLESS_SESSION_ID"), 10, 64); err == nil && sid > 0 {
		var taskID sql.NullInt64
		if err = db.QueryRow("SELECT task_id FROM sessions WHERE id = ?", sid).Scan(&taskID); err == nil && taskID.Valid {
			add(taskID.Int64, fmt.Sprintf("session ES-%d (ENDLESS_SESSION_ID)", sid))
		}
	}

	if ref := TaskIDFromWorktreePath(root); ref != "" {
		if id, err := strconv.ParseInt(strings.TrimPrefix(ref, "E-"), 10, 64); err == nil {
			add(id, "the worktree at "+root)
		}
	}
	return tasks, source
}
