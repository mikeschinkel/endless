package monitor

import (
	"database/sql"
	"testing"
)

// insertSession inserts a session row with explicit kind/state/project so the
// CountActiveBgAgents scope filters (kind=background, state='working',
// per-project) can be exercised directly.
func insertSession(t *testing.T, db *sql.DB, projectID int64, kindID int64, state string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, kind_id, started_at, last_activity)
		 VALUES (NULL, ?, 'claude', ?, ?, '2026-06-20T00:00:00', '2026-06-20T00:00:00')`,
		projectID, state, kindID,
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func TestCountActiveBgAgents_ScopesToProjectKindAndState(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	seedProject(t, db, 2, "p2", "/p2")
	seedTask(t, db, 10, 1, "t", "underway") // count target lives in project 1

	const bg = int64(2)   // background
	const tmux = int64(1) // foreground tmux

	// Should count: two working background agents in project 1.
	insertSession(t, db, 1, bg, "working")
	insertSession(t, db, 1, bg, "working")
	// Should NOT count: ended bg agent in project 1.
	insertSession(t, db, 1, bg, "ended")
	// Should NOT count: foreground (tmux) session in project 1.
	insertSession(t, db, 1, tmux, "working")
	// Should NOT count: working bg agent in a different project.
	insertSession(t, db, 2, bg, "working")

	n, err := CountActiveBgAgents(10)
	if err != nil {
		t.Fatalf("CountActiveBgAgents: %v", err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2 (only working bg agents in project 1)", n)
	}
}

func TestCountActiveBgAgents_NoneActive(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	seedTask(t, db, 10, 1, "t", "underway")

	n, err := CountActiveBgAgents(10)
	if err != nil {
		t.Fatalf("CountActiveBgAgents: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}
