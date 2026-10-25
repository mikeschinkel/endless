package events

import (
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// TestLiveSessionByProcessTx_AcceptsServerAlias (E-2428): a session bound to a
// pane under an earlier id of the running tmux server — recorded as its alias —
// is the live session in that pane.
func TestLiveSessionByProcessTx_AcceptsServerAlias(t *testing.T) {
	db := newSessionTasksTestDB(t)
	t.Cleanup(monitor.SetTestTmuxServer("0ae20eaa"))
	pid, err := monitor.SeedPaneProcess(db, "a6d29860", "%22")
	if err != nil {
		t.Fatalf("seed pane: %v", err)
	}
	res, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process_id, last_activity)
		 VALUES ('sess-restamped', NULL, 'claude', 'working', ?, '2026-10-24T00:00:00')`, pid,
	)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	want, _ := res.LastInsertId()

	if _, err = liveSessionByProcessTx(db, "%22"); err == nil {
		t.Fatalf("matched a pane bound under an unrelated server id")
	}

	if _, err = db.Exec(
		`INSERT INTO tmux_server_aliases (alias_uuid, server_uuid, recorded_at)
		 VALUES ('a6d29860', '0ae20eaa', '2026-10-24T00:00:00')`,
	); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	got, err := liveSessionByProcessTx(db, "%22")
	if err != nil || got != want {
		t.Errorf("liveSessionByProcessTx = %d, %v; want ES-%d", got, err, want)
	}
}
