package monitor

import (
	"fmt"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/sessionkind"
)

// liveCountForTask returns how many non-ended rows reference active_task_id.
func liveCountForTask(t *testing.T, taskID int64) int {
	t.Helper()
	db, err := DB()
	if err != nil {
		t.Fatalf("DB(): %v", err)
	}
	var n int
	if err = db.QueryRow(
		"SELECT count(*) FROM sessions WHERE active_task_id=? AND state != 'ended'",
		taskID,
	).Scan(&n); err != nil {
		t.Fatalf("count live for task %d: %v", taskID, err)
	}
	return n
}

// TestBindSessionToTask_EndsStalePanelessRowsSameTask is the E-1640 fix: when a
// fresh-UUID launch (resume / respawn / aborted spawn / /clear) binds to a task
// it already had a row for, the prior paneless row must be ended rather than
// left to accrue as a duplicate. Drives the real TouchSession→BindSessionToTask
// SessionStart flow twice with an empty TMUX_PANE and asserts exactly one live
// row remains for the task — the new one — with the old one 'ended'.
func TestBindSessionToTask_EndsStalePanelessRowsSameTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedTask(t, db, 42, 1, "task", "underway")
	t.Setenv("TMUX_PANE", "") // the empty-pane scenario this fix targets

	// First launch.
	if err := TouchSession("uuid-old", "claude", "", 1); err != nil {
		t.Fatalf("touch old: %v", err)
	}
	if err := BindSessionToTask("uuid-old", 1, 42); err != nil {
		t.Fatalf("bind old: %v", err)
	}

	// Second launch: fresh UUID, same task, still no pane.
	if err := TouchSession("uuid-new", "claude", "", 1); err != nil {
		t.Fatalf("touch new: %v", err)
	}
	if err := BindSessionToTask("uuid-new", 1, 42); err != nil {
		t.Fatalf("bind new: %v", err)
	}

	if got := liveCountForTask(t, 42); got != 1 {
		t.Fatalf("live rows for task 42 = %d, want 1", got)
	}
	if state, _, _ := sessionRow(t, db, "uuid-old"); state != "ended" {
		t.Errorf("uuid-old state = %q, want ended", state)
	}
	if state, _, _ := sessionRow(t, db, "uuid-new"); state != "working" {
		t.Errorf("uuid-new state = %q, want working", state)
	}
}

// TestBindSessionToTask_RepeatedLaunchesStayAtOneLiveRow generalizes the fix to
// several consecutive fresh-UUID launches: the live-row count must stay at 1, no
// matter how many times the session is relaunched against the same task.
func TestBindSessionToTask_RepeatedLaunchesStayAtOneLiveRow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedTask(t, db, 7, 1, "task", "underway")
	t.Setenv("TMUX_PANE", "")

	for i := 0; i < 5; i++ {
		uuid := fmt.Sprintf("uuid-%d", i) // distinct per iteration
		if err := TouchSession(uuid, "claude", "", 1); err != nil {
			t.Fatalf("touch %d: %v", i, err)
		}
		if err := BindSessionToTask(uuid, 1, 7); err != nil {
			t.Fatalf("bind %d: %v", i, err)
		}
		if got := liveCountForTask(t, 7); got != 1 {
			t.Fatalf("after launch %d: live rows = %d, want 1", i, got)
		}
	}
}

// TestBindSessionToTask_DoesNotEndBackgroundAgentSameTask guards the one row the
// fallback must never touch: a background agent (kind_id = background) carries a
// task's active_task_id with no pane, exactly matching the paneless predicate.
// Excluding it by kind is what keeps a foreground bind from killing a live bg
// agent working the same task.
func TestBindSessionToTask_DoesNotEndBackgroundAgentSameTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedTask(t, db, 42, 1, "task", "underway")
	t.Setenv("TMUX_PANE", "")

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, active_task_id, kind_id, started_at, last_activity)
		 VALUES ('uuid-bg', 1, 'claude', 'working', 42, ?, ?, ?)`,
		int64(sessionkind.SessionKindBackground), now, now,
	); err != nil {
		t.Fatalf("seed bg agent: %v", err)
	}

	if err := BindSessionToTask("uuid-fg", 1, 42); err != nil {
		t.Fatalf("bind fg: %v", err)
	}

	if state, _, _ := sessionRow(t, db, "uuid-bg"); state != "working" {
		t.Errorf("bg agent state = %q, want working (must not be ended)", state)
	}
}

// TestBindSessionToTask_DoesNotEndPanedRowSameTask documents the paneless
// scoping: a prior row that holds a real tmux pane for the same task is left for
// TouchSession's pane-collision path and is NOT ended by this fallback.
func TestBindSessionToTask_DoesNotEndPanedRowSameTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedTask(t, db, 42, 1, "task", "underway")
	t.Setenv("TMUX_PANE", "")

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, active_task_id, process, kind_id, started_at, last_activity)
		 VALUES ('uuid-paned', 1, 'claude', 'working', 42, '%9', ?, ?, ?)`,
		int64(sessionkind.SessionKindTmux), now, now,
	); err != nil {
		t.Fatalf("seed paned row: %v", err)
	}

	if err := BindSessionToTask("uuid-fg", 1, 42); err != nil {
		t.Fatalf("bind fg: %v", err)
	}

	if state, _, _ := sessionRow(t, db, "uuid-paned"); state != "working" {
		t.Errorf("paned row state = %q, want working (must not be ended)", state)
	}
}

// TestBindSessionToTask_DoesNotEndOtherTasksRows confirms the fallback is scoped
// to the bound task: a paneless live row for a DIFFERENT task is untouched.
func TestBindSessionToTask_DoesNotEndOtherTasksRows(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedTask(t, db, 42, 1, "task-a", "underway")
	seedTask(t, db, 43, 1, "task-b", "underway")
	t.Setenv("TMUX_PANE", "")

	if err := TouchSession("uuid-other", "claude", "", 1); err != nil {
		t.Fatalf("touch other: %v", err)
	}
	if err := BindSessionToTask("uuid-other", 1, 43); err != nil {
		t.Fatalf("bind other: %v", err)
	}

	if err := BindSessionToTask("uuid-fg", 1, 42); err != nil {
		t.Fatalf("bind fg: %v", err)
	}

	if state, _, _ := sessionRow(t, db, "uuid-other"); state != "working" {
		t.Errorf("other-task row state = %q, want working (must not be ended)", state)
	}
}
