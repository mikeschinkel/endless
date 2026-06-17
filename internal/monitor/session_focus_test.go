package monitor

import (
	"testing"

	"github.com/mikeschinkel/endless/internal/sessionkind"
)

// TestFocusedBgAgent picks the live background session whose active_task_id
// matches the child the coordinator is currently viewing (E-1552 derivation).
// It must ignore: bg agents on other children, the coordinator itself, tmux
// (foreground) rows on the same child, and ended bg rows.
func TestFocusedBgAgent(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	epicID := seedTask(t, db, 100, 1, "epic", "in_progress")
	childA := seedTask(t, db, 201, 1, "child-A", "in_progress")
	childB := seedTask(t, db, 202, 1, "child-B", "in_progress")

	insert := func(sid string, state string, taskID, kindID int64) {
		t.Helper()
		if _, err := db.Exec(
			`INSERT INTO sessions (session_id, project_id, platform, state, active_task_id, active_epic_id, kind_id, last_activity)
			 VALUES (?, 1, 'claude', ?, ?, ?, ?, '2026-06-16T00:00:00')`,
			sid, state, taskID, epicID, kindID,
		); err != nil {
			t.Fatalf("seed %s: %v", sid, err)
		}
	}

	// Coordinator is the tmux session viewing child A.
	insert("coord", "working", childA, int64(sessionkind.SessionKindTmux))
	// The bg agent we expect to find: background, on child A.
	insert("bg-A", "working", childA, int64(sessionkind.SessionKindBackground))
	// Distractors that must NOT be returned:
	insert("bg-B", "working", childB, int64(sessionkind.SessionKindBackground)) // other child
	insert("fg-A", "working", childA, int64(sessionkind.SessionKindTmux))       // tmux, not bg
	insert("bg-A-dead", "ended", childA, int64(sessionkind.SessionKindBackground))

	var coordID int64
	if err := db.QueryRow("SELECT id FROM sessions WHERE session_id='coord'").Scan(&coordID); err != nil {
		t.Fatalf("read coord id: %v", err)
	}

	got, err := FocusedBgAgent(coordID)
	if err != nil {
		t.Fatalf("FocusedBgAgent: %v", err)
	}
	if got == nil {
		t.Fatal("FocusedBgAgent = nil, want bg-A")
	}
	if got.SessionID != "bg-A" {
		t.Errorf("FocusedBgAgent = %q, want bg-A", got.SessionID)
	}
	if got.Kind != sessionkind.SessionKindBackground {
		t.Errorf("Kind = %v, want background", got.Kind)
	}
}

// TestFocusedBgAgent_NoMatch returns (nil, nil) when the coordinator has an
// active task but no background agent shares it — an ordinary state, not an
// error.
func TestFocusedBgAgent_NoMatch(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	childA := seedTask(t, db, 201, 1, "child-A", "in_progress")

	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, active_task_id, kind_id, last_activity)
		 VALUES ('coord', 1, 'claude', 'working', ?, ?, '2026-06-16T00:00:00')`,
		childA, int64(sessionkind.SessionKindTmux),
	); err != nil {
		t.Fatalf("seed coord: %v", err)
	}
	var coordID int64
	if err := db.QueryRow("SELECT id FROM sessions WHERE session_id='coord'").Scan(&coordID); err != nil {
		t.Fatalf("read coord id: %v", err)
	}

	got, err := FocusedBgAgent(coordID)
	if err != nil {
		t.Fatalf("FocusedBgAgent: %v", err)
	}
	if got != nil {
		t.Errorf("FocusedBgAgent = %+v, want nil", got)
	}
}
