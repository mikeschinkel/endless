package monitor

import (
	"database/sql"
	"testing"
)

// Relation ids, spelled as the fixtures' snSessionTaskRel takes them.
const (
	ownRelClaimed   = 1
	ownRelSurfaced  = 2
	ownRelRevisited = 3
	ownRelQueued    = 5
)

func ownSetFocus(t *testing.T, db *sql.DB, sessionID, taskID int64) {
	t.Helper()
	if _, err := db.Exec(`UPDATE sessions SET focus_task_id = ? WHERE id = ?`, taskID, sessionID); err != nil {
		t.Fatalf("set focus s=%d t=%d: %v", sessionID, taskID, err)
	}
}

func ownSetState(t *testing.T, db *sql.DB, sessionID int64, state string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE sessions SET state = ? WHERE id = ?`, state, sessionID); err != nil {
		t.Fatalf("set state s=%d: %v", sessionID, err)
	}
}

// ownBoard draws `viewer`'s board anchored on `focal` exactly as the renderer
// does — the real row query, then the ownership annotation — and returns it by id.
func ownBoard(t *testing.T, focal, viewer int64) map[int64]SessionStatusRow {
	t.Helper()
	rows, err := SessionStatusRows(focal, 0, false)
	if err != nil {
		t.Fatalf("SessionStatusRows(%d): %v", focal, err)
	}
	if err := AnnotateSessionStatusOwnership(rows, viewer, focal); err != nil {
		t.Fatalf("AnnotateSessionStatusOwnership: %v", err)
	}
	out := make(map[int64]SessionStatusRow, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

// ownFixture: sessions 1 (A), 2 (B), 3 (C) are live and have claimed 100, 101
// and 102; session 4 (D) claimed 103. Each claimer's claim is in session_tasks,
// as task.claimed writes it.
func ownFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	for _, id := range []int64{90, 100, 101, 102, 103, 150, 151, 152, 153} {
		snTask(t, db, id, 1, "ready", "now", "")
	}
	for s, task := range map[int64]int64{1: 100, 2: 101, 3: 102, 4: 103} {
		snSession(t, db, s, 1, task, "working")
		snSessionTaskRel(t, db, s, task, ownRelClaimed)
		ownSetFocus(t, db, s, task)
	}
	return db
}

// TestOwnership_WorkedCase is the plan's worked case. A's session filed E-150,
// B's session updated it. A keeps ownership; while B is focused on E-150 it
// shows on B's board and both boards carry the duplicate mark; once B's focus
// moves on, E-150 drops off B's board and A's mark clears.
func TestOwnership_WorkedCase(t *testing.T) {
	db := ownFixture(t)
	snSessionTaskRel(t, db, 1, 150, ownRelSurfaced)
	snSessionTaskRel(t, db, 2, 150, ownRelRevisited)
	ownSetFocus(t, db, 2, 150)

	a := ownBoard(t, 100, 1)[150]
	if a.OwnedElsewhere || !a.DuplicateWork {
		t.Errorf("A's board while B is focused on it: %+v, want shown with the duplicate mark", a)
	}
	b := ownBoard(t, 101, 2)[150]
	if b.OwnedElsewhere || !b.DuplicateWork || !b.Focused {
		t.Errorf("B's board while focused on it: %+v, want focused, shown, duplicate mark", b)
	}

	ownSetFocus(t, db, 2, 101)
	a = ownBoard(t, 100, 1)[150]
	if a.OwnedElsewhere || a.DuplicateWork {
		t.Errorf("A's board after B moved on: %+v, want shown, unmarked", a)
	}
	b = ownBoard(t, 101, 2)[150]
	if !b.OwnedElsewhere || b.Focused {
		t.Errorf("B's board after B moved on: %+v, want owned elsewhere", b)
	}
}

// TestOwnership_UpdateNeverTakesOwnership: however many times another session
// updates a filed task, the filer keeps it.
func TestOwnership_UpdateNeverTakesOwnership(t *testing.T) {
	db := ownFixture(t)
	snSessionTaskRel(t, db, 1, 150, ownRelSurfaced)
	snSessionTaskRel(t, db, 2, 150, ownRelRevisited)
	snSessionTaskRel(t, db, 3, 150, ownRelRevisited)

	if r := ownBoard(t, 100, 1)[150]; r.OwnedElsewhere || r.DuplicateWork {
		t.Errorf("filer's board: %+v, want shown, unmarked (two updaters do not make it ambiguous)", r)
	}
	for _, s := range []struct{ focal, viewer int64 }{{101, 2}, {102, 3}} {
		if r := ownBoard(t, s.focal, s.viewer)[150]; !r.OwnedElsewhere {
			t.Errorf("updater %d's board: %+v, want owned elsewhere", s.viewer, r)
		}
	}
}

// TestOwnership_SoleUpdaterOwns: with no live filer, the only live updater owns.
func TestOwnership_SoleUpdaterOwns(t *testing.T) {
	db := ownFixture(t)
	snSessionTaskRel(t, db, 2, 151, ownRelRevisited)
	// A has it only as queued, which is no claim to ownership.
	snSessionTaskRel(t, db, 1, 151, ownRelQueued)

	if r := ownBoard(t, 101, 2)[151]; r.OwnedElsewhere || r.DuplicateWork {
		t.Errorf("sole updater's board: %+v, want shown, unmarked", r)
	}
	if r := ownBoard(t, 100, 1)[151]; !r.OwnedElsewhere {
		t.Errorf("queueing board: %+v, want owned elsewhere", r)
	}
}

// TestOwnership_Ambiguous: no live filer and two live updaters — nobody owns it,
// and it stays on both boards with the duplicate mark.
func TestOwnership_Ambiguous(t *testing.T) {
	db := ownFixture(t)
	snSessionTaskRel(t, db, 2, 151, ownRelRevisited)
	snSessionTaskRel(t, db, 3, 151, ownRelRevisited)

	for _, s := range []struct{ focal, viewer int64 }{{101, 2}, {102, 3}} {
		r := ownBoard(t, s.focal, s.viewer)[151]
		if r.OwnedElsewhere || !r.DuplicateWork {
			t.Errorf("board %d: %+v, want shown with the duplicate mark", s.viewer, r)
		}
	}
}

// TestOwnership_EndedSessionReleases: a dead session owns nothing, so the moment
// it ends, its task comes back on a live board.
func TestOwnership_EndedSessionReleases(t *testing.T) {
	db := ownFixture(t)
	snSessionTaskRel(t, db, 4, 152, ownRelSurfaced)
	snSessionTaskRel(t, db, 1, 152, ownRelRevisited)

	if r := ownBoard(t, 100, 1)[152]; !r.OwnedElsewhere {
		t.Fatalf("while D is live: %+v, want owned elsewhere", r)
	}
	ownSetState(t, db, 4, "ended")
	if r := ownBoard(t, 100, 1)[152]; r.OwnedElsewhere || r.DuplicateWork {
		t.Errorf("after D ended: %+v, want back on A's board, unmarked", r)
	}
}

// TestOwnership_ClaimerIsNotADuplicate is the spawn flow: A files E-153, B
// claims it and is focused on it. B's claim is the expected case, not a second
// owner, so neither board is marked.
func TestOwnership_ClaimerIsNotADuplicate(t *testing.T) {
	db := ownFixture(t)
	snSessionTaskRel(t, db, 1, 153, ownRelSurfaced)
	snSession(t, db, 5, 1, 153, "working")
	snSessionTaskRel(t, db, 5, 153, ownRelClaimed)
	ownSetFocus(t, db, 5, 153)

	a := ownBoard(t, 100, 1)[153]
	if a.OwnedElsewhere || a.DuplicateWork || !a.InFlight {
		t.Errorf("filer's board: %+v, want the ⟳ row, shown, unmarked", a)
	}
	b := ownBoard(t, 153, 5)[153]
	if b.OwnedElsewhere || b.DuplicateWork || !b.Focused || !b.IsFocal {
		t.Errorf("claimer's board: %+v, want its own task, focused, unmarked", b)
	}
}

// TestOwnership_FrameRowsNeverHidden: the board's claimed task and its parent
// are never hidden or marked, whoever else touched them.
func TestOwnership_FrameRowsNeverHidden(t *testing.T) {
	db := ownFixture(t)
	if _, err := db.Exec(`UPDATE tasks SET parent_id = 90 WHERE id = 100`); err != nil {
		t.Fatalf("set parent: %v", err)
	}
	snSessionTaskRel(t, db, 2, 90, ownRelSurfaced)
	snSessionTaskRel(t, db, 2, 100, ownRelSurfaced)
	snSessionTaskRel(t, db, 3, 100, ownRelRevisited)

	board := ownBoard(t, 100, 1)
	for _, id := range []int64{90, 100} {
		r, ok := board[id]
		if !ok {
			t.Fatalf("E-%d missing from the board", id)
		}
		if r.OwnedElsewhere || r.DuplicateWork {
			t.Errorf("frame row E-%d: %+v, want never hidden or marked", id, r)
		}
	}
}

// TestOwnership_FocusFollowsViewer: Focused marks the viewer's focus only.
func TestOwnership_FocusFollowsViewer(t *testing.T) {
	db := ownFixture(t)
	snSessionTaskRel(t, db, 1, 150, ownRelSurfaced)
	ownSetFocus(t, db, 1, 150)
	board := ownBoard(t, 100, 1)
	if !board[150].Focused || board[100].Focused {
		t.Errorf("focus: E-150=%v E-100=%v, want only E-150", board[150].Focused, board[100].Focused)
	}
}

// TestOwnership_NoViewerAnnotatesNothing: a board that cannot identify its
// viewer suppresses nothing.
func TestOwnership_NoViewerAnnotatesNothing(t *testing.T) {
	db := ownFixture(t)
	snSessionTaskRel(t, db, 2, 150, ownRelSurfaced)
	snSessionTaskRel(t, db, 1, 150, ownRelRevisited)
	if r := ownBoard(t, 100, 0)[150]; r.OwnedElsewhere || r.DuplicateWork || r.Focused {
		t.Errorf("viewer 0: %+v, want unannotated", r)
	}
}
