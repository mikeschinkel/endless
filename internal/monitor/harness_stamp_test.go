package monitor

import (
	"database/sql"
	"testing"
)

// The two writes in this package that stamp tasks.changed_by_session bypass the
// event executor, so they cannot read Actor.Harness off an envelope — there is
// none. They ask agentenv directly instead, through stampableSession (E-2006).
//
// The gate is expected to be a NO-OP FOREVER: the only caller of either write is
// internal/hookcmd/claude.go, and `hook claude` returns before it reads stdin on
// an unsupported harness (E-1962), so an agent is the only thing that can reach
// them. These tests exist because that argument lives entirely in another
// package. They pin what the code DEPENDS ON, so a future caller from the CLI
// fails visibly here rather than silently stamping a human's edit with the
// session id the resolver credited it to — which is the E-1917 bug, re-entered
// through a door E-1917 did not cover.
//
// Both directions are asserted with t.Setenv rather than the ambient
// environment, because this suite is run both by an agent (harness present) and
// by a person at a shell (absent), and a test that agrees with whoever runs it
// asserts nothing.

// changedBySession reads the stamp for a task, NULL included.
func changedBySession(t *testing.T, db *sql.DB, taskID int64) sql.NullInt64 {
	t.Helper()
	var v sql.NullInt64
	if err := db.QueryRow(
		"SELECT changed_by_session FROM tasks WHERE id=?", taskID,
	).Scan(&v); err != nil {
		t.Fatalf("read changed_by_session for task %d: %v", taskID, err)
	}
	return v
}

// sessionRowID is the sessions.id the stamp's sub-select resolves to.
func sessionRowID(t *testing.T, db *sql.DB, sessionID string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		"SELECT id FROM sessions WHERE session_id=?", sessionID,
	).Scan(&id); err != nil {
		t.Fatalf("read sessions.id for %q: %v", sessionID, err)
	}
	return id
}

// TestStartWorkSession_StampsTheClaimingAgent is the real path, and a PASS here
// means the E-2006 gate changed nothing. `hook claude` runs only under a
// supported harness, so this is the only case that happens in production.
func TestStartWorkSession_StampsTheClaimingAgent(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 80, 1, "claimed by an agent", "ready")
	t.Setenv("TMUX_PANE", "%5")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")

	if err := StartWorkSession("sess-agent", 1, 80); err != nil {
		t.Fatalf("StartWorkSession: %v", err)
	}

	want := sessionRowID(t, db, "sess-agent")
	got := changedBySession(t, db, 80)
	if !got.Valid || got.Int64 != want {
		t.Errorf("changed_by_session = %v, want %d — the claiming agent must not "+
			"be notified about the status change it just caused", got, want)
	}
}

// TestStartWorkSession_DoesNotStampWithoutAHarness is the inverse demonstration.
// Nothing reaches this branch today; if something ever does, the acting party is
// a person, and stamping their own session id is what silences the notice they
// needed.
func TestStartWorkSession_DoesNotStampWithoutAHarness(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 81, 1, "claimed from a bare shell", "ready")
	t.Setenv("TMUX_PANE", "%5")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
	t.Setenv("__CFBundleIdentifier", "")

	if err := StartWorkSession("sess-human", 1, 81); err != nil {
		t.Fatalf("StartWorkSession: %v", err)
	}

	// The status still moves — the gate touches attribution only.
	if got := taskStatus(t, db, 81); got != "underway" {
		t.Errorf("task status = %q, want underway; the gate must not change the write", got)
	}
	if got := changedBySession(t, db, 81); got.Valid {
		t.Errorf("changed_by_session = %d, want NULL — no harness means a person "+
			"made this change, and a person suppresses nobody", got.Int64)
	}
}

// TestCompleteTask_StampsOnlyUnderAHarness covers the second write in one test,
// both directions, for the same reasons as the pair above.
func TestCompleteTask_StampsOnlyUnderAHarness(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedTask(t, db, 82, 1, "completed by an agent", "ready")
	seedTask(t, db, 83, 1, "completed from a bare shell", "ready")
	t.Setenv("TMUX_PANE", "%5")

	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	if err := StartWorkSession("sess-agent", 1, 82); err != nil {
		t.Fatalf("StartWorkSession 82: %v", err)
	}
	if err := CompleteTask("sess-agent", 82); err != nil {
		t.Fatalf("CompleteTask 82: %v", err)
	}
	want := sessionRowID(t, db, "sess-agent")
	if got := changedBySession(t, db, 82); !got.Valid || got.Int64 != want {
		t.Errorf("changed_by_session = %v, want %d", got, want)
	}

	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
	t.Setenv("__CFBundleIdentifier", "")
	if err := StartWorkSession("sess-human", 1, 83); err != nil {
		t.Fatalf("StartWorkSession 83: %v", err)
	}
	if err := CompleteTask("sess-human", 83); err != nil {
		t.Fatalf("CompleteTask 83: %v", err)
	}
	if got := taskStatus(t, db, 83); got != "confirmed" {
		t.Errorf("task status = %q, want confirmed; the gate must not change the write", got)
	}
	if got := changedBySession(t, db, 83); got.Valid {
		t.Errorf("changed_by_session = %d, want NULL", got.Int64)
	}
}
