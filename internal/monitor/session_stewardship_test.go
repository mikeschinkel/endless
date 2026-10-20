package monitor

import (
	"database/sql"
	"fmt"
	"testing"
)

// Relation ids, spelled as the fixtures' snSessionTaskRel takes them.
const (
	stewRelClaimed   = 1
	stewRelSurfaced  = 2
	stewRelRevisited = 3
	stewRelQueued    = 5
	stewRelSpawned   = 7 // E-2405
)

func stewSetFocus(t *testing.T, db *sql.DB, sessionID, taskID int64) {
	t.Helper()
	if _, err := db.Exec(`UPDATE sessions SET focus_task_id = ? WHERE id = ?`, taskID, sessionID); err != nil {
		t.Fatalf("set focus s=%d t=%d: %v", sessionID, taskID, err)
	}
}

func stewSetState(t *testing.T, db *sql.DB, sessionID int64, state string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE sessions SET state = ? WHERE id = ?`, state, sessionID); err != nil {
		t.Fatalf("set state s=%d: %v", sessionID, err)
	}
}

// stewBoard draws `viewer`'s board anchored on `focal` exactly as the renderer
// does — the real row query, then the stewardship annotation — and returns it by id.
func stewBoard(t *testing.T, focal, viewer int64) map[int64]SessionStatusRow {
	t.Helper()
	rows, err := SessionStatusRows(focal, 0, false)
	if err != nil {
		t.Fatalf("SessionStatusRows(%d): %v", focal, err)
	}
	if err := AnnotateSessionStatusStewardship(rows, viewer, focal); err != nil {
		t.Fatalf("AnnotateSessionStatusStewardship: %v", err)
	}
	out := make(map[int64]SessionStatusRow, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

// stewServer is the tmux server the fixture's sessions are bound to. It is
// observed, and panes %1..%9 are present on it, so a session bound with
// stewBindPane reads `live` — proven present, which is what stewardship counts
// (E-2293).
const stewServer = "stew-server"

// stewBindPane binds session s to pane %<s> on stewServer. pane overrides the
// address, e.g. "%404" for a pane that is not there.
func stewBindPane(t *testing.T, db *sql.DB, s int64, pane string) {
	t.Helper()
	if pane == "" {
		pane = fmt.Sprintf("%%%d", s)
	}
	pid := mustSeedPane(t, db, stewServer, pane)
	if _, err := db.Exec(`UPDATE sessions SET process_id = ? WHERE id = ?`, pid, s); err != nil {
		t.Fatalf("bind s=%d to %s: %v", s, pane, err)
	}
}

// stewFixture: sessions 1 (A), 2 (B), 3 (C) are live and have claimed 100, 101
// and 102; session 4 (D) claimed 103. Each claimer's claim is in session_tasks,
// as task.claimed writes it, and each is bound to a pane that is present.
func stewFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := withTestDB(t)
	panes := map[string]string{}
	for i := 1; i <= 9; i++ {
		panes[fmt.Sprintf("%%%d", i)] = "2.1.220"
	}
	t.Cleanup(SetTestTmuxObservation(stewServer, panes))
	seedProject(t, db, 1, "p1", "/p1")
	for _, id := range []int64{90, 100, 101, 102, 103, 150, 151, 152, 153} {
		snTask(t, db, id, 1, "ready", "now", "")
	}
	for s, task := range map[int64]int64{1: 100, 2: 101, 3: 102, 4: 103} {
		snSession(t, db, s, 1, task, "working")
		snSessionTaskRel(t, db, s, task, stewRelClaimed)
		stewSetFocus(t, db, s, task)
		stewBindPane(t, db, s, "")
	}
	return db
}

// TestStewardship_WorkedCase is the plan's worked case. A's session filed E-150,
// B's session updated it. A keeps stewardship; while B is focused on E-150 it
// shows on B's board and both boards carry the duplicate mark; once B's focus
// moves on, E-150 drops off B's board and A's mark clears.
func TestStewardship_WorkedCase(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 1, 150, stewRelSurfaced)
	snSessionTaskRel(t, db, 2, 150, stewRelRevisited)
	stewSetFocus(t, db, 2, 150)

	a := stewBoard(t, 100, 1)[150]
	if a.StewardedElsewhere || !a.DuplicateWork {
		t.Errorf("A's board while B is focused on it: %+v, want shown with the duplicate mark", a)
	}
	b := stewBoard(t, 101, 2)[150]
	if b.StewardedElsewhere || !b.DuplicateWork || !b.Focused {
		t.Errorf("B's board while focused on it: %+v, want focused, shown, duplicate mark", b)
	}

	stewSetFocus(t, db, 2, 101)
	a = stewBoard(t, 100, 1)[150]
	if a.StewardedElsewhere || a.DuplicateWork {
		t.Errorf("A's board after B moved on: %+v, want shown, unmarked", a)
	}
	b = stewBoard(t, 101, 2)[150]
	if !b.StewardedElsewhere || b.Focused {
		t.Errorf("B's board after B moved on: %+v, want stewarded elsewhere", b)
	}
}

// TestStewardship_UpdateNeverTakesStewardship: however many times another session
// updates a filed task, the filer keeps it.
func TestStewardship_UpdateNeverTakesStewardship(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 1, 150, stewRelSurfaced)
	snSessionTaskRel(t, db, 2, 150, stewRelRevisited)
	snSessionTaskRel(t, db, 3, 150, stewRelRevisited)

	if r := stewBoard(t, 100, 1)[150]; r.StewardedElsewhere || r.DuplicateWork {
		t.Errorf("filer's board: %+v, want shown, unmarked (two updaters do not make it ambiguous)", r)
	}
	for _, s := range []struct{ focal, viewer int64 }{{101, 2}, {102, 3}} {
		if r := stewBoard(t, s.focal, s.viewer)[150]; !r.StewardedElsewhere {
			t.Errorf("updater %d's board: %+v, want stewarded elsewhere", s.viewer, r)
		}
	}
}

// TestStewardship_SoleUpdaterStewards: with no live filer, the only live updater is the steward.
func TestStewardship_SoleUpdaterStewards(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 2, 151, stewRelRevisited)
	// A has it only as queued, which is no claim to stewardship.
	snSessionTaskRel(t, db, 1, 151, stewRelQueued)

	if r := stewBoard(t, 101, 2)[151]; r.StewardedElsewhere || r.DuplicateWork {
		t.Errorf("sole updater's board: %+v, want shown, unmarked", r)
	}
	if r := stewBoard(t, 100, 1)[151]; !r.StewardedElsewhere {
		t.Errorf("queueing board: %+v, want stewarded elsewhere", r)
	}
}

// TestStewardship_Ambiguous: no live filer and two live updaters — there is no steward,
// and it stays on both boards with the duplicate mark.
func TestStewardship_Ambiguous(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 2, 151, stewRelRevisited)
	snSessionTaskRel(t, db, 3, 151, stewRelRevisited)

	for _, s := range []struct{ focal, viewer int64 }{{101, 2}, {102, 3}} {
		r := stewBoard(t, s.focal, s.viewer)[151]
		if r.StewardedElsewhere || !r.DuplicateWork {
			t.Errorf("board %d: %+v, want shown with the duplicate mark", s.viewer, r)
		}
	}
}

// TestStewardship_EndedSessionReleases: a dead session stewards nothing, so the moment
// it ends, its task comes back on a live board.
func TestStewardship_EndedSessionReleases(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 4, 152, stewRelSurfaced)
	snSessionTaskRel(t, db, 1, 152, stewRelRevisited)

	if r := stewBoard(t, 100, 1)[152]; !r.StewardedElsewhere {
		t.Fatalf("while D is live: %+v, want stewarded elsewhere", r)
	}
	stewSetState(t, db, 4, "ended")
	if r := stewBoard(t, 100, 1)[152]; r.StewardedElsewhere || r.DuplicateWork {
		t.Errorf("after D ended: %+v, want back on A's board, unmarked", r)
	}
}

// TestStewardship_ClaimerIsNotADuplicate is the spawn flow: A files E-153, B
// claims it and is focused on it. B's claim makes it the task's owner, which is
// the expected case, not a second steward, so neither board is marked.
func TestStewardship_ClaimerIsNotADuplicate(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 1, 153, stewRelSurfaced)
	snSession(t, db, 5, 1, 153, "working")
	snSessionTaskRel(t, db, 5, 153, stewRelClaimed)
	stewSetFocus(t, db, 5, 153)
	stewBindPane(t, db, 5, "")

	a := stewBoard(t, 100, 1)[153]
	if a.StewardedElsewhere || a.DuplicateWork || !a.InFlight {
		t.Errorf("filer's board: %+v, want the ⟳ row, shown, unmarked", a)
	}
	b := stewBoard(t, 153, 5)[153]
	if b.StewardedElsewhere || b.DuplicateWork || !b.Focused || !b.IsFocal {
		t.Errorf("claimer's board: %+v, want its own task, focused, unmarked", b)
	}
}

// TestStewardship_FrameRowsNeverHidden: the board's claimed task and its parent
// are never hidden or marked, whoever else touched them.
func TestStewardship_FrameRowsNeverHidden(t *testing.T) {
	db := stewFixture(t)
	if _, err := db.Exec(`UPDATE tasks SET parent_id = 90 WHERE id = 100`); err != nil {
		t.Fatalf("set parent: %v", err)
	}
	snSessionTaskRel(t, db, 2, 90, stewRelSurfaced)
	snSessionTaskRel(t, db, 2, 100, stewRelSurfaced)
	snSessionTaskRel(t, db, 3, 100, stewRelRevisited)

	board := stewBoard(t, 100, 1)
	for _, id := range []int64{90, 100} {
		r, ok := board[id]
		if !ok {
			t.Fatalf("E-%d missing from the board", id)
		}
		if r.StewardedElsewhere || r.DuplicateWork {
			t.Errorf("frame row E-%d: %+v, want never hidden or marked", id, r)
		}
	}
}

// TestStewardship_FocusFollowsViewer: Focused marks the viewer's focus only.
func TestStewardship_FocusFollowsViewer(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 1, 150, stewRelSurfaced)
	stewSetFocus(t, db, 1, 150)
	board := stewBoard(t, 100, 1)
	if !board[150].Focused || board[100].Focused {
		t.Errorf("focus: E-150=%v E-100=%v, want only E-150", board[150].Focused, board[100].Focused)
	}
}

// TestStewardship_NoViewerAnnotatesNothing: a board that cannot identify its
// viewer suppresses nothing.
func TestStewardship_NoViewerAnnotatesNothing(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 2, 150, stewRelSurfaced)
	snSessionTaskRel(t, db, 1, 150, stewRelRevisited)
	if r := stewBoard(t, 100, 0)[150]; r.StewardedElsewhere || r.DuplicateWork || r.Focused {
		t.Errorf("viewer 0: %+v, want unannotated", r)
	}
}

func stewHide(t *testing.T, sessionID, taskID int64) {
	t.Helper()
	if _, err := HideSessionTasks(sessionID, []int64{taskID}); err != nil {
		t.Fatalf("hide s=%d t=%d: %v", sessionID, taskID, err)
	}
}

// TestStewardship_HiddenRevisiterExcluded is E-2204's reported case (E-1814): two
// live updaters, one of which hid the task. The hider gives up its claim, so the
// task is not ambiguous and the other updater is its steward, unmarked.
func TestStewardship_HiddenRevisiterExcluded(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 2, 151, stewRelRevisited)
	snSessionTaskRel(t, db, 3, 151, stewRelRevisited)
	stewHide(t, 3, 151)

	if r := stewBoard(t, 101, 2)[151]; r.StewardedElsewhere || r.DuplicateWork {
		t.Errorf("remaining updater's board: %+v, want shown, unmarked", r)
	}
	if r := stewBoard(t, 102, 3)[151]; !r.StewardedElsewhere || r.DuplicateWork {
		t.Errorf("hider's board: %+v, want stewarded elsewhere, unmarked", r)
	}
}

// TestStewardship_HiddenSurfacerExcluded: a filer that hides its own task gives up
// stewardship too — hide means "not mine" — so the sole updater is its steward.
func TestStewardship_HiddenSurfacerExcluded(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 1, 150, stewRelSurfaced)
	snSessionTaskRel(t, db, 2, 150, stewRelRevisited)
	stewHide(t, 1, 150)

	if r := stewBoard(t, 101, 2)[150]; r.StewardedElsewhere || r.DuplicateWork {
		t.Errorf("updater's board: %+v, want shown, unmarked (it is the steward now)", r)
	}
	if r := stewBoard(t, 100, 1)[150]; !r.StewardedElsewhere {
		t.Errorf("hiding filer's board: %+v, want stewarded elsewhere", r)
	}
}

// TestStewardship_HiddenNonFocuserExcluded: a hider that has moved its focus away
// leaves no trace — neither a steward nor a focuser — so the filer's row is
// unmarked.
func TestStewardship_HiddenNonFocuserExcluded(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 1, 150, stewRelSurfaced)
	snSessionTaskRel(t, db, 2, 150, stewRelSurfaced)
	stewHide(t, 2, 150)

	if r := stewBoard(t, 100, 1)[150]; r.StewardedElsewhere || r.DuplicateWork {
		t.Errorf("filer's board: %+v, want shown, unmarked (two filers, one hid it)", r)
	}
}

// TestStewardship_HiddenButFocusedCounts: focus overrides hide. A session that hid
// a task and then put it back in focus is touching it again, which is exactly
// when duplicate work is possible, so both boards carry the mark.
func TestStewardship_HiddenButFocusedCounts(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 1, 150, stewRelSurfaced)
	snSessionTaskRel(t, db, 2, 150, stewRelRevisited)
	stewHide(t, 2, 150)
	stewSetFocus(t, db, 2, 150)

	if r := stewBoard(t, 100, 1)[150]; r.StewardedElsewhere || !r.DuplicateWork {
		t.Errorf("filer's board: %+v, want shown with the duplicate mark", r)
	}
	if r := stewBoard(t, 101, 2)[150]; r.StewardedElsewhere || !r.DuplicateWork || !r.Focused {
		t.Errorf("hider's board while focused: %+v, want focused, shown, duplicate mark", r)
	}
}

// stewBindUnreachable binds session s to a pane on a tmux server this process
// never reached and has no recorded socket for. Its liveness reads `unknown`.
func stewBindUnreachable(t *testing.T, db *sql.DB, s int64) {
	t.Helper()
	pid := mustSeedPane(t, db, "unreachable-server", "%265")
	if _, err := db.Exec(`UPDATE sessions SET process_id = ? WHERE id = ?`, pid, s); err != nil {
		t.Fatalf("bind s=%d: %v", s, err)
	}
}

// stewBindGone binds session s to a pane on a tmux server whose recorded socket
// now refuses connections — gone, as after a tmux restart (E-2300). Its
// liveness reads `dead`.
func stewBindGone(t *testing.T, db *sql.DB, s int64) {
	t.Helper()
	seedTmuxServer(t, db, goneServer, goneSocket)
	stubSocketProbes(t, map[string]socketProbe{goneSocket: {refused: true}})
	pid := mustSeedPane(t, db, goneServer, "%265")
	if _, err := db.Exec(`UPDATE sessions SET process_id = ? WHERE id = ?`, pid, s); err != nil {
		t.Fatalf("bind s=%d: %v", s, err)
	}
}

// TestStewardship_GoneServerSessionDoesNotSteward is E-2293's measured case:
// E-1993/E-1995 were withheld from their own epic's board by a session bound to
// a tmux server that no longer exists. E-2293 fixed it by requiring `live`;
// E-2300 fixes it at the root — that server is judged gone, so the session
// reads `dead` — and stewardship returns to the claim guard's test.
func TestStewardship_GoneServerSessionDoesNotSteward(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 4, 150, stewRelSurfaced)
	snSessionTaskRel(t, db, 1, 150, stewRelRevisited)
	stewSetState(t, db, 4, "idle")
	stewBindGone(t, db, 4)

	if r := stewBoard(t, 100, 1)[150]; r.StewardedElsewhere || r.DuplicateWork {
		t.Errorf("A's board: %+v, want shown, unmarked (the filer's server is gone)", r)
	}
}

// TestStewardship_UnreachableSessionStewards: a session whose server could not
// be reached is present, as it is to the claim guard (E-2300: one presence test
// everywhere). Unprovable is not gone.
func TestStewardship_UnreachableSessionStewards(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 4, 150, stewRelSurfaced)
	snSessionTaskRel(t, db, 1, 150, stewRelRevisited)
	stewSetState(t, db, 4, "idle")
	stewBindUnreachable(t, db, 4)

	if r := stewBoard(t, 100, 1)[150]; !r.StewardedElsewhere {
		t.Errorf("A's board: %+v, want stewarded elsewhere (the filer is not provably gone)", r)
	}
}

// TestStewardship_DeadPaneDoesNotSteward: a session whose row still reads
// `working` but whose pane was observed gone stewards nothing.
func TestStewardship_DeadPaneDoesNotSteward(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 4, 150, stewRelSurfaced)
	snSessionTaskRel(t, db, 1, 150, stewRelRevisited)
	stewBindPane(t, db, 4, "%404")

	if r := stewBoard(t, 100, 1)[150]; r.StewardedElsewhere {
		t.Errorf("A's board: %+v, want shown (the filer's pane is gone)", r)
	}
}

// TestStewardship_IdlePresentSessionStillStewards: idle is not absence. A filer
// whose pane is there keeps its task, so stewardship does not dissolve when
// every session is waiting on the user.
func TestStewardship_IdlePresentSessionStillStewards(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 4, 150, stewRelSurfaced)
	snSessionTaskRel(t, db, 1, 150, stewRelRevisited)
	stewSetState(t, db, 1, "idle")
	stewSetState(t, db, 4, "idle")

	if r := stewBoard(t, 100, 1)[150]; !r.StewardedElsewhere {
		t.Errorf("A's board: %+v, want stewarded elsewhere (the idle filer is present)", r)
	}
}

// TestStewardship_OneClaimerClearsDuplicate: once a present session has claimed
// the task, the duplicate warning has done its job — even where the ladder alone
// would still call it ambiguous.
func TestStewardship_OneClaimerClearsDuplicate(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 2, 151, stewRelRevisited)
	snSessionTaskRel(t, db, 3, 151, stewRelRevisited)
	snSession(t, db, 5, 1, 151, "working")
	snSessionTaskRel(t, db, 5, 151, stewRelClaimed)
	stewBindPane(t, db, 5, "")

	for _, s := range []struct{ focal, viewer int64 }{{101, 2}, {102, 3}} {
		if r := stewBoard(t, s.focal, s.viewer)[151]; r.DuplicateWork {
			t.Errorf("board %d: %+v, want no duplicate mark (E-151 has its own session)", s.viewer, r)
		}
	}
}

// TestStewardship_GoneClaimerKeepsDuplicate: a claimer whose server is gone does
// not clear the warning — that would be the ghost problem in the other
// direction.
func TestStewardship_GoneClaimerKeepsDuplicate(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 2, 151, stewRelRevisited)
	snSessionTaskRel(t, db, 3, 151, stewRelRevisited)
	snSession(t, db, 5, 1, 151, "working")
	snSessionTaskRel(t, db, 5, 151, stewRelClaimed)
	stewBindGone(t, db, 5)

	if r := stewBoard(t, 101, 2)[151]; !r.DuplicateWork {
		t.Errorf("B's board: %+v, want the duplicate mark (the claimer's server is gone)", r)
	}
}

// TestStewardship_UnreachableClaimerClearsDuplicate: a claimer that is merely
// unreachable still holds the task — the claim guard refuses a second claim on
// it — so it clears the warning as a live one does (E-2300).
func TestStewardship_UnreachableClaimerClearsDuplicate(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 2, 151, stewRelRevisited)
	snSessionTaskRel(t, db, 3, 151, stewRelRevisited)
	snSession(t, db, 5, 1, 151, "working")
	snSessionTaskRel(t, db, 5, 151, stewRelClaimed)
	stewBindUnreachable(t, db, 5)

	if r := stewBoard(t, 101, 2)[151]; r.DuplicateWork {
		t.Errorf("B's board: %+v, want no duplicate mark (the claimer is not provably gone)", r)
	}
}

// TestStewardship_TwoClaimersMarkDuplicate: two present sessions claiming one
// task is the collision ◫ exists for, and it is raised whatever the ladder says.
func TestStewardship_TwoClaimersMarkDuplicate(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 1, 153, stewRelSurfaced)
	for _, s := range []int64{5, 6} {
		snSession(t, db, s, 1, 153, "working")
		snSessionTaskRel(t, db, s, 153, stewRelClaimed)
		stewBindPane(t, db, s, "")
	}
	if r := stewBoard(t, 100, 1)[153]; !r.DuplicateWork {
		t.Errorf("filer's board: %+v, want the duplicate mark (two claimants)", r)
	}
}

// TestStewardship_PinOutranksSteward: `session unhide --task` on a row another
// session stewards pins it to the viewer's board.
func TestStewardship_PinOutranksSteward(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 4, 150, stewRelSurfaced)
	snSessionTaskRel(t, db, 1, 150, stewRelRevisited)
	if _, err := db.Exec(
		`INSERT INTO session_pinned_tasks (session_id, task_id, pinned_at) VALUES (1, 150, 'now')`,
	); err != nil {
		t.Fatalf("pin: %v", err)
	}

	r := stewBoard(t, 100, 1)[150]
	if r.StewardedElsewhere || !r.Pinned {
		t.Errorf("A's board: %+v, want pinned and shown", r)
	}
	if r := stewBoard(t, 103, 4)[150]; r.StewardedElsewhere || r.Pinned {
		t.Errorf("steward's board: %+v, want shown, unpinned (a pin is per-session)", r)
	}
}

// TestStewardship_SpawnerStewardsLikeAFiler pins E-2405's "stewardship counts a
// spawned session as it counts a filing one": A spawned E-150 and B merely
// updated it. A keeps it, exactly as in TestStewardship_UpdateNeverTakesStewardship
// with a filer — the update does not move it to B.
func TestStewardship_SpawnerStewardsLikeAFiler(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 1, 150, stewRelSpawned)
	snSessionTaskRel(t, db, 2, 150, stewRelRevisited)

	if r := stewBoard(t, 100, 1)[150]; r.StewardedElsewhere || r.DuplicateWork {
		t.Errorf("spawner's board: %+v, want shown, unmarked", r)
	}
	if r := stewBoard(t, 101, 2)[150]; !r.StewardedElsewhere {
		t.Errorf("updater's board: %+v, want stewarded elsewhere", r)
	}
}

// TestStewardship_SpawnerAndFilerShareARung: a spawner and a filer sit on the
// SAME rung of the ladder, so one of each is ambiguous, just as two filers are.
// If spawned had a rung of its own (above or below surfaced) one of them would
// win outright instead.
func TestStewardship_SpawnerAndFilerShareARung(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 1, 151, stewRelSurfaced)
	snSessionTaskRel(t, db, 2, 151, stewRelSpawned)

	for _, s := range []struct{ focal, viewer int64 }{{100, 1}, {101, 2}} {
		r := stewBoard(t, s.focal, s.viewer)[151]
		if r.StewardedElsewhere || !r.DuplicateWork {
			t.Errorf("board %d: %+v, want shown with the duplicate mark", s.viewer, r)
		}
	}
}

// TestStewardship_SpawnedTaskClaimedBySpawnee is the spawn flow with the new
// relation: A spawns E-153 and session 5 claims it. The claimer is the owner and
// is left out of the count, so A stewards it — shown on A's board as ⟳ doing,
// unmarked — mirroring TestStewardship_ClaimerIsNotADuplicate for a filer.
func TestStewardship_SpawnedTaskClaimedBySpawnee(t *testing.T) {
	db := stewFixture(t)
	snSessionTaskRel(t, db, 1, 153, stewRelSpawned)
	snSession(t, db, 5, 1, 153, "working")
	snSessionTaskRel(t, db, 5, 153, stewRelClaimed)
	stewSetFocus(t, db, 5, 153)
	stewBindPane(t, db, 5, "")

	a := stewBoard(t, 100, 1)[153]
	if a.StewardedElsewhere || a.DuplicateWork || !a.InFlight {
		t.Errorf("spawner's board: %+v, want the ⟳ row, shown, unmarked", a)
	}
}
