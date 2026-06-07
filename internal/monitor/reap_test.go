package monitor

import (
	"os/exec"
	"testing"
)

// TestReapDeadTmuxPanes_NullsProcessOnEnded pins Layer A for the reaper:
// rows the reaper marks ended also have their process NULL'd, so a
// later tmux server restart that reuses pane ids can't pull these dead
// rows back into a lookup (E-1530).
//
// Skips when `tmux` isn't on $PATH — the reaper opportunistically calls
// `tmux list-panes` and bails as a no-op without it, so there's nothing
// to assert.
func TestReapDeadTmuxPanes_NullsProcessOnEnded(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available; reaper is a no-op")
	}
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")

	// Seed a row with a synthetic tmux pane id that cannot match any
	// live pane on the host (high index, %999990+ range used elsewhere
	// for the same reason). The reaper sees it as not in the live set
	// and ends it.
	const deadPane = "%999993"
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, '2026-05-20T00:00:00')`,
		"sess-dead", 1, deadPane,
	); err != nil {
		t.Fatalf("seed dead: %v", err)
	}

	if err := ReapDeadTmuxPanes(1); err != nil {
		t.Fatalf("ReapDeadTmuxPanes: %v", err)
	}

	var state string
	var process *string
	if err := db.QueryRow(
		"SELECT state, process FROM sessions WHERE session_id = ?", "sess-dead",
	).Scan(&state, &process); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if state != "ended" {
		t.Errorf("state = %q, want ended", state)
	}
	if process != nil {
		t.Errorf("process = %q, want NULL after reap", *process)
	}
}
