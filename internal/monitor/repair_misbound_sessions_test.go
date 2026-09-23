package monitor

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/schema"
	_ "modernc.org/sqlite"
)

// repairFixtureDB builds a DB with the real schema and one project.
func repairFixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "endless.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enable fks: %v", err)
	}
	if err = schema.Migrate(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err = db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/p')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return db
}

// seedBoundSession inserts a task (once), a sessions row bound to it, and the
// first activity row that records where the session was launched. `launchDir`
// of "" seeds no activity row at all, which is the "no launch directory ever
// recorded" case.
func seedBoundSession(t *testing.T, db *sql.DB, rowID int64, uuid string, taskID int64, launchDir, lastActivity string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT OR IGNORE INTO tasks (id, project_id, title, status) VALUES (?, 1, 't', 'underway')",
		taskID,
	); err != nil {
		t.Fatalf("seed task %d: %v", taskID, err)
	}
	if _, err := db.Exec(
		"INSERT INTO sessions (id, session_id, project_id, platform, state, task_id, last_activity) "+
			"VALUES (?, ?, 1, 'claude', 'ended', ?, ?)",
		rowID, uuid, taskID, lastActivity,
	); err != nil {
		t.Fatalf("seed session %d: %v", rowID, err)
	}
	if launchDir == "" {
		return
	}
	// Two events, oldest first: the repair must read the FIRST working_dir, so
	// the second one deliberately says somewhere else (the `/cd` trap).
	for _, dir := range []string{launchDir, "/somewhere/else"} {
		if _, err := db.Exec(
			"INSERT INTO activity (project_id, source, working_dir, session_context) "+
				`VALUES (1, 'claude', ?, json_object('session_id', ?))`,
			dir, uuid,
		); err != nil {
			t.Fatalf("seed activity for %s: %v", uuid, err)
		}
	}
}

// runRepair drops the write-once trigger, repairs, and re-creates it — the exact
// sequence the change file performs — and returns the verdicts.
func runRepair(t *testing.T, db *sql.DB) []TaskRepair {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err = tx.Exec("DROP TRIGGER IF EXISTS sessions_task_id_write_once"); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	repairs, err := RepairMisboundSessions(tx)
	if err != nil {
		tx.Rollback()
		t.Fatalf("repair: %v", err)
	}
	if _, err = tx.Exec(`CREATE TRIGGER IF NOT EXISTS sessions_task_id_write_once
BEFORE UPDATE OF task_id ON sessions
WHEN OLD.task_id IS NOT NULL AND NEW.task_id IS NOT OLD.task_id
BEGIN
    SELECT RAISE(ABORT, 'sessions.task_id is write-once');
END`); err != nil {
		t.Fatalf("recreate trigger: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return repairs
}

func taskIDOf(t *testing.T, db *sql.DB, rowID int64) *int64 {
	t.Helper()
	var taskID *int64
	if err := db.QueryRow("SELECT task_id FROM sessions WHERE id = ?", rowID).Scan(&taskID); err != nil {
		t.Fatalf("read session %d: %v", rowID, err)
	}
	return taskID
}

// TestRepairMisboundSessions_E1732Shape is the acceptance case E-1983's plan
// names: task 1732 carries three rows, one launched inside its own worktree and
// two launched in the main checkout. The worktree-launched row is the genuine
// one and must be KEPT even though it is the OLDEST and its state is `ended`;
// keep-the-newest would discard the row the conversation is attached to and keep
// the husk.
func TestRepairMisboundSessions_E1732Shape(t *testing.T) {
	db := repairFixtureDB(t)
	old := "2026-07-04T05:04:36"
	seedBoundSession(t, db, 879, "26ba3f7d", 1732, "/p/.endless/worktrees/e-1732", old)
	seedBoundSession(t, db, 881, "ee6eda74", 1732, "/p", old)
	seedBoundSession(t, db, 882, "9f45bd76", 1732, "/p", old)

	repairs := runRepair(t, db)

	if len(repairs) != 1 || repairs[0].Verdict != RepairVerdictRepaired {
		t.Fatalf("verdicts = %+v, want one %q", repairs, RepairVerdictRepaired)
	}
	if got := taskIDOf(t, db, 879); got == nil || *got != 1732 {
		t.Errorf("the worktree-launched row lost its task: %v", got)
	}
	for _, rowID := range []int64{881, 882} {
		if got := taskIDOf(t, db, rowID); got != nil {
			t.Errorf("row %d still bound to %d; it was launched in the main checkout", rowID, *got)
		}
	}
}

// TestRepairMisboundSessions_ReadsFirstWorkingDir pins the trap: a `/cd` changes
// a session's per-line cwd but does not move the session, so lineage must come
// from the FIRST working_dir. seedBoundSession writes a second, contradicting
// activity row for every session precisely so a last-wins reading fails here.
func TestRepairMisboundSessions_ReadsFirstWorkingDir(t *testing.T) {
	db := repairFixtureDB(t)
	seedBoundSession(t, db, 1, "a", 500, "/p/.endless/worktrees/e-500", "2026-01-01T00:00:00")
	seedBoundSession(t, db, 2, "b", 500, "/p", "2026-01-01T00:00:00")

	runRepair(t, db)

	if got := taskIDOf(t, db, 1); got == nil || *got != 500 {
		t.Errorf("first working_dir was ignored; row 1 = %v, want 500", got)
	}
	if got := taskIDOf(t, db, 2); got != nil {
		t.Errorf("row 2 still bound to %d", *got)
	}
}

// TestRepairMisboundSessions_LeavesInstancesAlone: rows that all launched in the
// same directory are one session's successive instances — a /clear mints a new
// harness id — which is E-2063's population, not this bug.
func TestRepairMisboundSessions_LeavesInstancesAlone(t *testing.T) {
	db := repairFixtureDB(t)
	seedBoundSession(t, db, 1, "a", 600, "/p", "2026-01-01T00:00:00")
	seedBoundSession(t, db, 2, "b", 600, "/p", "2026-01-01T00:00:00")

	repairs := runRepair(t, db)

	if len(repairs) != 1 || repairs[0].Verdict != RepairVerdictInstances {
		t.Fatalf("verdicts = %+v, want one %q", repairs, RepairVerdictInstances)
	}
	for _, rowID := range []int64{1, 2} {
		if got := taskIDOf(t, db, rowID); got == nil || *got != 600 {
			t.Errorf("row %d was unbound; instances must be left alone", rowID)
		}
	}
}

// TestRepairMisboundSessions_UndecidedChangesNothing: launch directories
// disagree and none is the task's own worktree, so nothing says which row is
// genuine. Unbinding on a guess could destroy the only binding the task has.
func TestRepairMisboundSessions_UndecidedChangesNothing(t *testing.T) {
	db := repairFixtureDB(t)
	seedBoundSession(t, db, 1, "a", 700, "/p", "2026-01-01T00:00:00")
	seedBoundSession(t, db, 2, "b", 700, "/p/.endless/worktrees/e-999", "2026-01-01T00:00:00")

	repairs := runRepair(t, db)

	if len(repairs) != 1 || repairs[0].Verdict != RepairVerdictUndecided {
		t.Fatalf("verdicts = %+v, want one %q", repairs, RepairVerdictUndecided)
	}
	for _, rowID := range []int64{1, 2} {
		if got := taskIDOf(t, db, rowID); got == nil || *got != 700 {
			t.Errorf("row %d was changed on an undecided verdict", rowID)
		}
	}
}

// TestRepairMisboundSessions_LeavesLiveSessionBound: a row active minutes ago is
// somebody's live session. Taking its task away mid-turn is not recoverable
// under write-once, so the repair reports it and leaves it.
func TestRepairMisboundSessions_LeavesLiveSessionBound(t *testing.T) {
	db := repairFixtureDB(t)
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	seedBoundSession(t, db, 1, "a", 800, "/p/.endless/worktrees/e-800", "2026-01-01T00:00:00")
	seedBoundSession(t, db, 2, "b", 800, "/p", now)

	repairs := runRepair(t, db)

	if got := taskIDOf(t, db, 2); got == nil || *got != 800 {
		t.Fatalf("a live session was unbound mid-turn: row 2 = %v", got)
	}
	if len(repairs) != 1 || len(repairs[0].Unbound) != 0 {
		t.Fatalf("verdicts = %+v, want nothing unbound", repairs)
	}
}

// TestRepairMisboundSessions_SecondRunIsANoOp: the change file is gated by its
// _schema_version marker, but the logic must be idempotent on its own — a second
// pass finds the mis-bound rows already NULL, so they belong to no task's row set.
func TestRepairMisboundSessions_SecondRunIsANoOp(t *testing.T) {
	db := repairFixtureDB(t)
	seedBoundSession(t, db, 1, "a", 900, "/p/.endless/worktrees/e-900", "2026-01-01T00:00:00")
	seedBoundSession(t, db, 2, "b", 900, "/p", "2026-01-01T00:00:00")

	first := runRepair(t, db)
	if len(first) != 1 || len(first[0].Unbound) != 1 {
		t.Fatalf("first run = %+v, want one row unbound", first)
	}

	second := runRepair(t, db)
	if len(second) != 0 {
		t.Fatalf("second run = %+v, want no tasks in the population", second)
	}
	if got := taskIDOf(t, db, 1); got == nil || *got != 900 {
		t.Errorf("second run disturbed the kept row: %v", got)
	}
}

// TestSessionIsLive pins the freshness window, including the two shapes that
// must NOT read as live: an empty timestamp, and one this code cannot parse.
func TestSessionIsLive(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"unparseable", "last tuesday", false},
		{"ancient", "2026-01-01T00:00:00", false},
		{"now, no zone", time.Now().UTC().Format("2006-01-02T15:04:05"), true},
		{"now, RFC3339", time.Now().UTC().Format(time.RFC3339), true},
	}
	for _, c := range cases {
		if got := sessionIsLive(c.in); got != c.want {
			t.Errorf("%s: sessionIsLive(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}
