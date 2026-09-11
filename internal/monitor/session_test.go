package monitor

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/sessionstate"
)

// freshSessionsDB returns a DB with schema.sql applied, seeded with project
// rows for ids 1 and 2 so sessions inserts referencing them satisfy the FK.
// This helper is for tests that work with the DB directly (e.g. the
// ListLiveSessions SQL-shape test below); tests of the singleton-using
// public wrappers (TouchSession, etc.) use withTestDB + seedProject.
func freshSessionsDB(t *testing.T) *sql.DB {
	t.Helper()
	db := freshDB(t)
	applySchema(t, db)
	for id := int64(1); id <= 2; id++ {
		suffix := string(rune('0' + id))
		seedProject(t, db, id, "proj-test-"+suffix, "/tmp/proj-test-"+suffix)
	}
	return db
}

func sessionRow(t *testing.T, db *sql.DB, sessionID string) (state, process, platform string) {
	t.Helper()
	err := db.QueryRow(
		`SELECT s.state, COALESCE(p.address, ''), s.platform
		 FROM sessions s LEFT JOIN processes p ON p.id = s.process_id
		 WHERE s.session_id=?`,
		sessionID,
	).Scan(&state, &process, &platform)
	if err != nil {
		t.Fatalf("read row %q: %v", sessionID, err)
	}
	return
}

// TestTouchSession_InsertCreatesIdle is the SessionStart-happy-path shape: no
// prior row, first touch creates one with state='idle' and the supplied
// process. Exercises the public TouchSession wrapper (and thus monitor.DB())
// via the withTestDB seam — previously this test targeted the unexported
// touchSessionDB carve-out (E-1506).
//
// The INSERT default was `needs_input` until E-2091; see
// TestInitSession_InsertCreatesIdle for why it moved.
func TestTouchSession_InsertCreatesIdle(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := TouchSession("sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch: %v", err)
	}
	state, process, platform := sessionRow(t, db, "sess-A")
	if state != "idle" {
		t.Errorf("state = %q, want idle", state)
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
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	// Empty TMUX_PANE at SessionStart (the E-1408 scenario).
	if err := TouchSession("sess-A", "claude", "", 1); err != nil {
		t.Fatalf("touch 1: %v", err)
	}
	_, process, _ := sessionRow(t, db, "sess-A")
	if process != "" {
		t.Errorf("after empty touch, process = %q, want empty", process)
	}
	// Next event has the real pane id; row must repair.
	if err := TouchSession("sess-A", "claude", "%7", 1); err != nil {
		t.Fatalf("touch 2: %v", err)
	}
	_, process, _ = sessionRow(t, db, "sess-A")
	if process != "%7" {
		t.Errorf("after second touch, process = %q, want %%7", process)
	}
	// A subsequent empty touch must not erase the known value.
	if err := TouchSession("sess-A", "claude", "", 1); err != nil {
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
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := TouchSession("sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch 1: %v", err)
	}
	if _, err := db.Exec(
		"UPDATE sessions SET state='working' WHERE session_id=?",
		"sess-A",
	); err != nil {
		t.Fatalf("force working: %v", err)
	}
	if err := TouchSession("sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch 2: %v", err)
	}
	state, _, _ := sessionRow(t, db, "sess-A")
	if state != "working" {
		t.Errorf("state = %q, want working (UPDATE clobbered it)", state)
	}
}

// TestTouchSession_RevivesEndedRow encodes the E-1686 fix: once a row is
// 'ended' (here forced directly, standing in for EndSession / the pane reaper /
// collision invalidation), the session's own next hook must lift it back to a
// live state. Before the fix the UPDATE branch never touched state, so the row
// stayed 'ended' and every `state != 'ended'` reader hid the still-live session.
func TestTouchSession_RevivesEndedRow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := TouchSession("sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch 1: %v", err)
	}
	if _, err := db.Exec(
		"UPDATE sessions SET state='ended' WHERE session_id=?", "sess-A",
	); err != nil {
		t.Fatalf("force ended: %v", err)
	}

	// The session's continued activity — the proof-of-life that must revive it.
	if err := TouchSession("sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch 2: %v", err)
	}
	state, _, _ := sessionRow(t, db, "sess-A")
	if state != "idle" {
		t.Errorf("state = %q, want idle (ended row not revived)", state)
	}
}

// TestTouchSession_RevivesOnlyEndedNotLiveStates guards the CASE: revival fires
// ONLY for 'ended'. Every live state must pass through an UPDATE unchanged so
// the dedicated lifecycle helpers stay authoritative — `prompted` above all,
// since TouchSession runs on every hook event and clobbering it there would
// clear the prompt on the very event that set it (E-2091).
func TestTouchSession_RevivesOnlyEndedNotLiveStates(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	for _, live := range sessionstate.Get(sessionstate.Live) {
		sid := "sess-" + live
		if err := TouchSession(sid, "claude", "%5", 1); err != nil {
			t.Fatalf("touch 1 (%s): %v", live, err)
		}
		if _, err := db.Exec(
			"UPDATE sessions SET state=? WHERE session_id=?", live, sid,
		); err != nil {
			t.Fatalf("force %s: %v", live, err)
		}
		if err := TouchSession(sid, "claude", "%5", 1); err != nil {
			t.Fatalf("touch 2 (%s): %v", live, err)
		}
		state, _, _ := sessionRow(t, db, sid)
		if state != live {
			t.Errorf("live state %q clobbered by touch: got %q", live, state)
		}
	}
}

// TestTouchSession_ReusedPaneDoesNotReviveOther is the E-1530 safety guard:
// reviving must be gated on the session_id conflict target, NOT a bare pane
// match. A DIFFERENT session_id arriving on a pane whose prior occupant is
// 'ended' (e.g. a reused %N after a tmux server restart) must take the INSERT
// path and leave the prior ended row ended.
func TestTouchSession_ReusedPaneDoesNotReviveOther(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := TouchSession("sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch A: %v", err)
	}
	if _, err := db.Exec(
		"UPDATE sessions SET state='ended' WHERE session_id=?", "sess-A",
	); err != nil {
		t.Fatalf("force A ended: %v", err)
	}

	// A different session_id reuses pane %5.
	if err := TouchSession("sess-B", "claude", "%5", 1); err != nil {
		t.Fatalf("touch B: %v", err)
	}

	stateA, _, _ := sessionRow(t, db, "sess-A")
	if stateA != "ended" {
		t.Errorf("prior occupant A revived by a different session's touch: A.state=%q, want ended (E-1530)", stateA)
	}
	stateB, _, _ := sessionRow(t, db, "sess-B")
	if stateB == "ended" {
		t.Errorf("new occupant B.state = ended; should be live")
	}
}

// TestTouchSession_PaneReattachOverwritesProcess: same session_id appearing
// on a different pane (e.g. Claude --resume in a new window) updates
// process to the new value.
func TestTouchSession_PaneReattachOverwritesProcess(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := TouchSession("sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch 1: %v", err)
	}
	if err := TouchSession("sess-A", "claude", "%12", 1); err != nil {
		t.Fatalf("touch 2: %v", err)
	}
	_, process, _ := sessionRow(t, db, "sess-A")
	if process != "%12" {
		t.Errorf("process = %q, want %%12 (reattach not tracked)", process)
	}
}

// Collision invalidation was REMOVED by E-1898; the test that used to live here
// asserted a new session on an occupied pane ended the prior occupant. That
// write killed live sessions whenever the "collision" was really a pane id
// reissued by a restarted tmux server (E-1468), and identity now makes the
// reissue case impossible, so there is nothing left to invalidate. The
// replacement guarantee — a same-server collision leaves BOTH rows standing —
// is pinned by TestTouchSession_SameServerCollisionLeavesPriorRowAlone in
// tmux_lookup_ghost_test.go, alongside the rest of the E-1530 family.

// TestTouchSession_EmptyProcessDoesNotInvalidate: an empty incoming
// process (TMUX_PANE unset) must NOT trigger collision invalidation —
// otherwise a non-tmux event would mark every tmux row ended.
func TestTouchSession_EmptyProcessDoesNotInvalidate(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := TouchSession("sess-A", "claude", "%5", 1); err != nil {
		t.Fatalf("touch A: %v", err)
	}
	if err := TouchSession("sess-B", "claude", "", 1); err != nil {
		t.Fatalf("touch B: %v", err)
	}
	stateA, _, _ := sessionRow(t, db, "sess-A")
	if stateA == "ended" {
		t.Errorf("empty-process touch invalidated unrelated session: A.state=%q", stateA)
	}
}

// TestTouchSession_RejectsEmptySessionID confirms the input-validation
// branch of the public wrapper. The unexported helper (now inlined) had
// no such check; covering it here is the reason the seam matters.
func TestTouchSession_RejectsEmptySessionID(t *testing.T) {
	withTestDB(t)
	err := TouchSession("", "claude", "%5", 1)
	if err == nil {
		t.Fatal("TouchSession(\"\", ...) returned nil, want error")
	}
}

// TestTouchSession_RejectsEmptyPlatform mirrors the session-id check for
// the platform argument.
func TestTouchSession_RejectsEmptyPlatform(t *testing.T) {
	withTestDB(t)
	err := TouchSession("sess-A", "", "%5", 1)
	if err == nil {
		t.Fatal("TouchSession(..., \"\", ...) returned nil, want error")
	}
}

// TestGetActiveSession_RoundTripsEpic pins epic_id through the GetActiveSession
// read path: a row carrying an epic round-trips to EpicID alongside TaskID, and
// a row without one reports nil rather than zero.
//
// Named RoundTripsKindAndEpic until E-2074, when sessions.kind_id and the
// SessionKind enum went with background agents. Epic context is the half that
// survived — a session still works a child under an epic.
func TestGetActiveSession_RoundTripsEpic(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	epicID := seedTask(t, db, 100, 1, "epic", "underway")
	childID := seedTask(t, db, 137, 1, "child", "underway")

	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, task_id, epic_id, last_activity)
		 VALUES ('epic-1', 1, 'claude', 'working', ?, ?, '2026-06-16T00:00:00')`,
		childID, epicID,
	); err != nil {
		t.Fatalf("seed epic session: %v", err)
	}

	got, err := GetActiveSession("epic-1")
	if err != nil {
		t.Fatalf("GetActiveSession(epic-1): %v", err)
	}
	if got.EpicID == nil || *got.EpicID != epicID {
		t.Errorf("EpicID = %v, want %d", got.EpicID, epicID)
	}
	if got.TaskID == nil || *got.TaskID != childID {
		t.Errorf("TaskID = %v, want %d", got.TaskID, childID)
	}

	// A row with no epic context: EpicID must be nil, not 0.
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, task_id, last_activity)
		 VALUES ('plain-1', 1, 'claude', 'working', ?, '2026-06-16T00:00:00')`,
		childID,
	); err != nil {
		t.Fatalf("seed plain session: %v", err)
	}
	plain, err := GetActiveSession("plain-1")
	if err != nil {
		t.Fatalf("GetActiveSession(plain-1): %v", err)
	}
	if plain.EpicID != nil {
		t.Errorf("default EpicID = %v, want nil", plain.EpicID)
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
			`INSERT INTO sessions (session_id, project_id, platform, state, process_id, last_activity)
		 VALUES (?, ?, 'claude', ?, ?, ?)`, r.sid, r.projectID, r.state, mustSeedPane(t, db, TestServerUUID, r.process), "2026-05-20T00:00:00"); err != nil {
			t.Fatalf("seed %s: %v", r.sid, err)
		}
	}

	// The real ListLiveSessions uses DB() — running the same SQL directly
	// against the local db confirms the filter shape without competing
	// with the singleton seam. ListLiveSessions itself is covered by the
	// sessionquerycmd binary-integration tests in Phase 2.
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
