package monitor

import (
	"database/sql"
	"testing"
)

func sessionProject(t *testing.T, db *sql.DB, guid string) int64 {
	t.Helper()
	var id sql.NullInt64
	if err := db.QueryRow(`SELECT project_id FROM sessions WHERE session_id = ?`, guid).Scan(&id); err != nil {
		t.Fatalf("read project of %s: %v", guid, err)
	}
	return id.Int64
}

// TestBindSessionToTask_ProjectFollowsTask is E-2358: the cwd auto-bind stamps
// the session with the TASK's project, not the one the hook's cwd resolved to.
// A session started in the endless repo to work a go-cfgstore task was
// recorded under endless, so every project-scoped lookup run from go-cfgstore
// missed it. The next hook event (TouchSession) must not move it back.
func TestBindSessionToTask_ProjectFollowsTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "endless", "/tmp/endless")
	seedProject(t, db, 2, "go-cfgstore", "/tmp/go-cfgstore")
	seedTask(t, db, 42, 2, "task", "underway")
	t.Setenv("TMUX_PANE", "")

	// The UPDATE path: a session the hook already recorded under project 1.
	if err := TouchSession("uuid-1", "claude", "", 1); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := BindSessionToTask("uuid-1", 42); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got := sessionProject(t, db, "uuid-1"); got != 2 {
		t.Errorf("after bind: project_id = %d, want 2 (the task's)", got)
	}
	if err := TouchSession("uuid-1", "claude", "", 1); err != nil {
		t.Fatalf("touch again: %v", err)
	}
	if got := sessionProject(t, db, "uuid-1"); got != 2 {
		t.Errorf("after the next hook event: project_id = %d, want 2 still", got)
	}

	// The INSERT path: a session no hook has recorded yet.
	if err := BindSessionToTask("uuid-2", 42); err != nil {
		t.Fatalf("bind new: %v", err)
	}
	if got := sessionProject(t, db, "uuid-2"); got != 2 {
		t.Errorf("inserted: project_id = %d, want 2", got)
	}
}

// TestBindSessionToTask_RefusesMissingTask: with no task there is no project
// to take, and nothing to bind to.
func TestBindSessionToTask_RefusesMissingTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	t.Setenv("TMUX_PANE", "")
	if err := BindSessionToTask("uuid-1", 999); err == nil {
		t.Fatal("bind to a task that does not exist succeeded")
	}
}

// TestListLiveSessionsAllProjects is the query half of E-2358: every project's
// live sessions, each row carrying its own project's resolved path, while the
// scoped query still answers for one project.
func TestListLiveSessionsAllProjects(t *testing.T) {
	db := withTestDB(t)
	rootA := tempProjectRoot(t)
	rootB := tempProjectRoot(t)
	seedProject(t, db, 1, "a", rootA)
	seedProject(t, db, 2, "b", rootB)
	t.Setenv("TMUX_PANE", "")
	for guid, project := range map[string]int64{"in-a": 1, "in-b": 2} {
		if err := TouchSession(guid, "claude", "", project); err != nil {
			t.Fatalf("touch %s: %v", guid, err)
		}
	}

	all, err := ListLiveSessionsAllProjects()
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, s := range all {
		paths[s.SessionID] = s.ProjectPath
	}
	if len(paths) != 2 || paths["in-a"] != rootA || paths["in-b"] != rootB {
		t.Errorf("all projects: got %v, want in-a→%s and in-b→%s", paths, rootA, rootB)
	}

	one, err := ListLiveSessions(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].SessionID != "in-b" || one[0].ProjectPath != rootB {
		t.Errorf("project 2 only: got %+v, want just in-b at %s", one, rootB)
	}
}
