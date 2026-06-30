package monitor

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/sessionkind"
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
// and the supplied process. Exercises the public TouchSession wrapper
// (and thus monitor.DB()) via the withTestDB seam — previously this test
// targeted the unexported touchSessionDB carve-out (E-1506).
func TestTouchSession_InsertCreatesNeedsInput(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := TouchSession("sess-A", "claude", "%5", 1); err != nil {
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
	if state != "needs_input" {
		t.Errorf("state = %q, want needs_input (ended row not revived)", state)
	}
}

// TestTouchSession_RevivesOnlyEndedNotLiveStates guards the CASE: revival fires
// ONLY for 'ended'. A live state (working/idle/needs_input) must pass through
// an UPDATE unchanged so the dedicated lifecycle helpers stay authoritative.
func TestTouchSession_RevivesOnlyEndedNotLiveStates(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	for _, live := range []string{"working", "idle", "needs_input"} {
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

// TestTouchSession_CollisionInvalidationMarksPriorEnded: a new session
// arriving on a pane already held by a different live session marks the
// prior occupant 'ended' in the same transaction. A pane can only host
// one harness at a time, so the prior must be dead.
func TestTouchSession_CollisionInvalidationMarksPriorEnded(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if err := TouchSession("sess-A", "claude", "%5", 1); err != nil {
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
	if err := TouchSession("sess-B", "claude", "%5", 1); err != nil {
		t.Fatalf("touch B: %v", err)
	}

	stateA, processA, _ := sessionRow(t, db, "sess-A")
	if stateA != "ended" {
		t.Errorf("prior occupant A.state = %q, want ended", stateA)
	}
	// A's process is NULL'd along with the state flip (E-1530, Layer A).
	// Required because tmux pane ids (`%N`) are reused after a tmux server
	// restart — leaving the value behind lets the ghost row win the lookup
	// for the next server's pane with the same id.
	if processA != "" {
		t.Errorf("prior occupant A.process = %q, want NULL (E-1530)", processA)
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

// TestGetActiveSession_RoundTripsKindAndEpic pins the E-1571 columns through
// the GetActiveSession read path: a background-agent row with active_epic_id
// set round-trips to ActiveEpicID + Kind=background, and a default foreground
// row reports Kind=tmux with a nil ActiveEpicID.
func TestGetActiveSession_RoundTripsKindAndEpic(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	epicID := seedTask(t, db, 100, 1, "epic", "underway")
	childID := seedTask(t, db, 137, 1, "child", "underway")

	// Background agent: process NULL, kind_id=background, epic + child set.
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, active_task_id, active_epic_id, kind_id, last_activity)
		 VALUES ('bg-1', 1, 'claude', 'working', ?, ?, ?, '2026-06-16T00:00:00')`,
		childID, epicID, int64(sessionkind.SessionKindBackground),
	); err != nil {
		t.Fatalf("seed bg session: %v", err)
	}

	got, err := GetActiveSession("bg-1")
	if err != nil {
		t.Fatalf("GetActiveSession(bg-1): %v", err)
	}
	if got.Kind != sessionkind.SessionKindBackground {
		t.Errorf("Kind = %v, want background", got.Kind)
	}
	if got.ActiveEpicID == nil || *got.ActiveEpicID != epicID {
		t.Errorf("ActiveEpicID = %v, want %d", got.ActiveEpicID, epicID)
	}
	if got.ActiveTaskID == nil || *got.ActiveTaskID != childID {
		t.Errorf("ActiveTaskID = %v, want %d", got.ActiveTaskID, childID)
	}

	// Foreground default row: kind defaults to tmux, no epic context.
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, active_task_id, last_activity)
		 VALUES ('fg-1', 1, 'claude', 'working', ?, '2026-06-16T00:00:00')`,
		childID,
	); err != nil {
		t.Fatalf("seed fg session: %v", err)
	}
	fg, err := GetActiveSession("fg-1")
	if err != nil {
		t.Fatalf("GetActiveSession(fg-1): %v", err)
	}
	if fg.Kind != sessionkind.SessionKindTmux {
		t.Errorf("default Kind = %v, want tmux", fg.Kind)
	}
	if fg.ActiveEpicID != nil {
		t.Errorf("default ActiveEpicID = %v, want nil", fg.ActiveEpicID)
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
