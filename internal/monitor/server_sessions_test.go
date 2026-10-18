package monitor

import (
	"testing"
)

// TestSessionsOnPanes: the live sessions bound to the asked panes on the
// asked server, with their task and state — not an ended one, not one on the
// same pane id of another server, not one on a pane that was not asked.
func TestSessionsOnPanes(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	for _, id := range []int{12, 13, 14, 15} {
		if _, err := db.Exec(`INSERT INTO tasks (id, project_id, title, status) VALUES (?, 1, 't', 'underway')`, id); err != nil {
			t.Fatalf("seed task: %v", err)
		}
	}
	seed := func(sid, state string, task any, server, pane string) {
		t.Helper()
		if _, err := db.Exec(
			`INSERT INTO sessions (session_id, project_id, platform, state, task_id, process_id, last_activity)
			 VALUES (?, 1, 'claude', ?, ?, ?, '2026-10-18T00:00:00')`,
			sid, state, task, mustSeedPane(t, db, server, pane)); err != nil {
			t.Fatalf("seed %s: %v", sid, err)
		}
	}
	seed("working", "working", 12, TestServerUUID, "%1")
	seed("idle", "idle", nil, TestServerUUID, "%2")
	seed("ended", "ended", 13, TestServerUUID, "%3")
	seed("elsewhere", "working", 14, "other-server", "%1")
	seed("unasked", "working", 15, TestServerUUID, "%9")

	got, err := SessionsOnPanes(TestServerUUID, []string{"%1", "%2", "%3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v, want the working and idle sessions only", got)
	}
	if got[0].Pane != "%1" || got[0].TaskID != 12 || got[0].State != "working" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Pane != "%2" || got[1].TaskID != 0 || got[1].State != "idle" {
		t.Errorf("second = %+v", got[1])
	}
	if got, _ = SessionsOnPanes("", []string{"%1"}); len(got) != 0 {
		t.Errorf("a server with no identity matched %+v", got)
	}
}
