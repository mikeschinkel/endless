package events

import (
	"database/sql"
	"testing"
)

// TestTaskClaimed_SessionProjectFollowsTask is E-2358: claiming (and binding)
// a task re-stamps the session's project with the task's, so a session that
// started in one repo and claimed another repo's task is found by lookups in
// the task's project.
func TestTaskClaimed_SessionProjectFollowsTask(t *testing.T) {
	db := newSessionTasksTestDB(t)
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	const ts = "2026-05-16T00:00:00"
	mustExec(`INSERT INTO projects (id, name, path, status, created_at, updated_at)
	          VALUES (2, 'other', '/tmp/other', 'active', ?, ?)`, ts, ts)
	mustExec(`INSERT INTO tasks (id, project_id, title, status, phase, created_at, updated_at)
	          VALUES (101, 2, 'theirs', 'ready', 'now', ?, ?)`, ts, ts)
	mustExec(`INSERT INTO sessions (id, session_id, project_id, platform, state, started_at, last_activity)
	          VALUES (42, 'uuid-42', 1, 'claude', 'idle', ?, ?)`, ts, ts)
	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}

	project := func() int64 {
		t.Helper()
		var id sql.NullInt64
		if err := db.QueryRow(`SELECT project_id FROM sessions WHERE id = 42`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id.Int64
	}

	if _, err := dispatch(db, taskClaimedEvent(t, 101, actor), nil); err != nil {
		t.Fatalf("dispatch claim: %v", err)
	}
	if got := project(); got != 2 {
		t.Errorf("after claiming a project-2 task: project_id = %d, want 2", got)
	}
}
