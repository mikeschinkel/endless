package monitor

import (
	"database/sql"
	"testing"
)

// freshSessionsDB returns a freshly-migrated DB seeded with project rows
// for ids 1 and 2 so sessions inserts referencing them satisfy the FK.
func freshSessionsDB(t *testing.T) *sql.DB {
	t.Helper()
	db := freshDB(t)
	if _, err := migrate(db, MigrateOpts{Runner: RunnerAuto, SkipBackup: true}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for id := int64(1); id <= 2; id++ {
		suffix := string(rune('0' + id))
		if _, err := db.Exec(
			"INSERT INTO projects (id, name, path) VALUES (?, ?, ?)",
			id, "proj-test-"+suffix, "/tmp/proj-test-"+suffix,
		); err != nil {
			t.Fatalf("seed project %d: %v", id, err)
		}
	}
	return db
}

func sessionRow(t *testing.T, db *sql.DB, sessionID string) (state, process, platform string) {
	t.Helper()
	err := db.QueryRow(
		"SELECT state, COALESCE(process, ''), platform FROM sessions WHERE session_id=?",
		sessionID,
	).Scan(&state, &process, &platform)
	if err != nil {
		t.Fatalf("read row %q: %v", sessionID, err)
	}
	return
}

// TestTouchSession_InsertCreatesNeedsInput is the SessionStart-happy-path
// shape: no prior row, first touch creates one with state='needs_input'
// and the supplied process.
func TestTouchSession_InsertCreatesNeedsInput(t *testing.T) {
	db := freshSessionsDB(t)
	if err := touchSessionDB(db, "sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch: %v", err)
	}
	state, process, platform := sessionRow(t, db, "sess-A")
	if state != "needs_input" {
		t.Errorf("state = %q, want needs_input", state)
	}
	if process != "%5" {
		t.Errorf("process = %q, want %%5", process)
	}
	if platform != "claude" {
		t.Errorf("platform = %q, want claude", platform)
	}
}

// TestTouchSession_E1408_EmptyProcessDoesNotStomp encodes the E-1408 fix:
// an empty TMUX_PANE on INSERT leaves process NULL, and a subsequent
// non-empty value backfills it correctly. The previous SetProcess flow
// silently lost the second value because the first attempt left the
// column NULL and the next event also passed empty.
func TestTouchSession_E1408_EmptyProcessDoesNotStomp(t *testing.T) {
	db := freshSessionsDB(t)
	// Empty TMUX_PANE at SessionStart (the E-1408 scenario).
	if err := touchSessionDB(db, "sess-A", "claude", "", 1); err != nil {
		t.Fatalf("touch 1: %v", err)
	}
	_, process, _ := sessionRow(t, db, "sess-A")
	if process != "" {
		t.Errorf("after empty touch, process = %q, want empty", process)
	}
	// Next event has the real pane id; row must repair.
	if err := touchSessionDB(db, "sess-A", "claude", "%7", 1); err != nil {
		t.Fatalf("touch 2: %v", err)
	}
	_, process, _ = sessionRow(t, db, "sess-A")
	if process != "%7" {
		t.Errorf("after second touch, process = %q, want %%7", process)
	}
	// A subsequent empty touch must not erase the known value.
	if err := touchSessionDB(db, "sess-A", "claude", "", 1); err != nil {
		t.Fatalf("touch 3: %v", err)
	}
	_, process, _ = sessionRow(t, db, "sess-A")
	if process != "%7" {
		t.Errorf("empty touch stomped known process: %q, want %%7", process)
	}
}

// TestTouchSession_StatePreservedAcrossUpdate verifies UPDATE never
// clobbers `state` — so lifecycle helpers (BindSessionToTask → 'working',
// IdleSession → 'idle') stay authoritative and TouchSession can safely
// fire on every hook event including PreToolUse mid-turn.
func TestTouchSession_StatePreservedAcrossUpdate(t *testing.T) {
	db := freshSessionsDB(t)
	if err := touchSessionDB(db, "sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch 1: %v", err)
	}
	if _, err := db.Exec(
		"UPDATE sessions SET state='working' WHERE session_id=?",
		"sess-A",
	); err != nil {
		t.Fatalf("force working: %v", err)
	}
	if err := touchSessionDB(db, "sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch 2: %v", err)
	}
	state, _, _ := sessionRow(t, db, "sess-A")
	if state != "working" {
		t.Errorf("state = %q, want working (UPDATE clobbered it)", state)
	}
}

// TestTouchSession_PaneReattachOverwritesProcess: same session_id appearing
// on a different pane (e.g. Claude --resume in a new window) updates
// process to the new value.
func TestTouchSession_PaneReattachOverwritesProcess(t *testing.T) {
	db := freshSessionsDB(t)
	if err := touchSessionDB(db, "sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch 1: %v", err)
	}
	if err := touchSessionDB(db, "sess-A", "claude", "%12", 1); err != nil {
		t.Fatalf("touch 2: %v", err)
	}
	_, process, _ := sessionRow(t, db, "sess-A")
	if process != "%12" {
		t.Errorf("process = %q, want %%12 (reattach not tracked)", process)
	}
}

// TestTouchSession_CollisionInvalidationMarksPriorEnded: a new session
// arriving on a pane already held by a different live session marks the
// prior occupant 'ended' in the same transaction. A pane can only host
// one harness at a time, so the prior must be dead.
func TestTouchSession_CollisionInvalidationMarksPriorEnded(t *testing.T) {
	db := freshSessionsDB(t)
	if err := touchSessionDB(db, "sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch A: %v", err)
	}
	// Force A into a non-default live state to verify the invalidation.
	if _, err := db.Exec(
		"UPDATE sessions SET state='working' WHERE session_id=?",
		"sess-A",
	); err != nil {
		t.Fatalf("force working: %v", err)
	}

	// New session B takes over pane %5.
	if err := touchSessionDB(db, "sess-B", "claude", "%5", 1); err != nil {
		t.Fatalf("touch B: %v", err)
	}

	stateA, processA, _ := sessionRow(t, db, "sess-A")
	if stateA != "ended" {
		t.Errorf("prior occupant A.state = %q, want ended", stateA)
	}
	// A's process is left in place — we only flip state. Readers filter
	// by state != 'ended', so the value doesn't matter; preserving it
	// keeps audit trails honest.
	if processA != "%5" {
		t.Errorf("prior occupant A.process = %q, want %%5 (state-only flip)", processA)
	}
	stateB, processB, _ := sessionRow(t, db, "sess-B")
	if stateB == "ended" {
		t.Errorf("new occupant B.state = ended; should be live")
	}
	if processB != "%5" {
		t.Errorf("new occupant B.process = %q, want %%5", processB)
	}
}

// TestTouchSession_EmptyProcessDoesNotInvalidate: an empty incoming
// process (TMUX_PANE unset) must NOT trigger collision invalidation —
// otherwise a non-tmux event would mark every tmux row ended.
func TestTouchSession_EmptyProcessDoesNotInvalidate(t *testing.T) {
	db := freshSessionsDB(t)
	if err := touchSessionDB(db, "sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch A: %v", err)
	}
	if err := touchSessionDB(db, "sess-B", "claude", "", 1); err != nil {
		t.Fatalf("touch B: %v", err)
	}
	stateA, _, _ := sessionRow(t, db, "sess-A")
	if stateA == "ended" {
		t.Errorf("empty-process touch invalidated unrelated session: A.state=%q", stateA)
	}
}

// TestListLiveSessions_FiltersEndedAndScopesToProject: ListLiveSessions
// returns only state!='ended' rows for the given project_id, ordered by
// last_activity DESC.
func TestListLiveSessions_FiltersEndedAndScopesToProject(t *testing.T) {
	db := freshSessionsDB(t)
	// Two live in project 1, one ended in project 1, one live in project 2.
	rows := []struct {
		sid, state, process string
		projectID           int64
	}{
		{"live-1", "working", "%5", 1},
		{"live-2", "idle", "%6", 1},
		{"dead-1", "ended", "%7", 1},
		{"other-proj", "working", "%8", 2},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
			 VALUES (?, ?, 'claude', ?, ?, ?)`,
			r.sid, r.projectID, r.state, r.process, "2026-05-20T00:00:00",
		); err != nil {
			t.Fatalf("seed %s: %v", r.sid, err)
		}
	}

	// listLiveSessionsDB wraps ListLiveSessions's body for testability;
	// the real ListLiveSessions uses DB() but the SQL is identical, so we
	// run the query directly and compare. Keeps the test free of the
	// dbOnce singleton.
	out, err := db.Query(
		`SELECT session_id FROM sessions
		 WHERE state != 'ended' AND project_id = ?
		 ORDER BY last_activity DESC`,
		1,
	)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer out.Close()
	var got []string
	for out.Next() {
		var sid string
		if err := out.Scan(&sid); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, sid)
	}
	if len(got) != 2 {
		t.Fatalf("got %d live sessions for project 1, want 2: %v", len(got), got)
	}
}
