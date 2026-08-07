package monitor

import (
	"testing"
)

// TestSessionHiddenTasks_PerSessionPair is the core guarantee of E-1914: hiding
// is a property of the (session, task) PAIR, never of the task. Two sessions on
// the same task must see independent hide state — a regression to a global flag
// would show up here and essentially nowhere else.
func TestSessionHiddenTasks_PerSessionPair(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	snTask(t, db, 500, 1, "ready", "now", "")
	snTask(t, db, 501, 1, "ready", "now", "")
	snSession(t, db, 9001, 1, 500, "working")
	snSession(t, db, 9002, 1, 500, "working")

	if _, err := HideSessionTasks(9001, []int64{500}); err != nil {
		t.Fatalf("hide: %v", err)
	}

	hidden, err := SessionHiddenTasks(9001)
	if err != nil {
		t.Fatalf("SessionHiddenTasks(9001): %v", err)
	}
	if _, ok := hidden[500]; !ok {
		t.Errorf("session 9001: task 500 should be hidden, got %v", hidden)
	}
	if _, ok := hidden[501]; ok {
		t.Errorf("session 9001: task 501 was never hidden, got %v", hidden)
	}

	other, err := SessionHiddenTasks(9002)
	if err != nil {
		t.Fatalf("SessionHiddenTasks(9002): %v", err)
	}
	if len(other) != 0 {
		t.Errorf("session 9002 must be untouched by 9001's hide, got %v", other)
	}
}

// TestHideUnhideAreIdempotent pins the no-op contract: re-hiding an already
// hidden task is not an error and does not restamp hidden_at (--only-hidden
// orders by how long something has been suppressed, so the ORIGINAL time is the
// meaningful one). Unhiding something that was never hidden is likewise a no-op.
func TestHideUnhideAreIdempotent(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	snTask(t, db, 500, 1, "ready", "now", "")
	snSession(t, db, 9001, 1, 500, "working")

	n, err := HideSessionTasks(9001, []int64{500})
	if err != nil || n != 1 {
		t.Fatalf("first hide = (%d, %v), want (1, nil)", n, err)
	}
	first, _ := SessionHiddenTasks(9001)

	n, err = HideSessionTasks(9001, []int64{500})
	if err != nil {
		t.Fatalf("second hide errored: %v", err)
	}
	if n != 0 {
		t.Errorf("second hide newly-hid %d rows, want 0 (no-op)", n)
	}
	again, _ := SessionHiddenTasks(9001)
	if again[500] != first[500] {
		t.Errorf("re-hide restamped hidden_at: %q → %q", first[500], again[500])
	}

	n, err = UnhideSessionTasks(9001, []int64{500})
	if err != nil || n != 1 {
		t.Fatalf("unhide = (%d, %v), want (1, nil)", n, err)
	}
	n, err = UnhideSessionTasks(9001, []int64{500})
	if err != nil {
		t.Fatalf("second unhide errored: %v", err)
	}
	if n != 0 {
		t.Errorf("second unhide removed %d rows, want 0 (no-op)", n)
	}
	if h, _ := SessionHiddenTasks(9001); len(h) != 0 {
		t.Errorf("after unhide, hidden set should be empty, got %v", h)
	}
}

// TestAnnotateSessionStatusHidden_ViewerScoped proves the annotation layers the
// VIEWER's hides onto a viewer-agnostic row set, and that viewer 0 — no session
// resolved, e.g. outside tmux — suppresses nothing. A listing that cannot
// identify who is looking must show everything rather than apply some other
// session's hides.
func TestAnnotateSessionStatusHidden_ViewerScoped(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	snTask(t, db, 500, 1, "ready", "now", "")
	snTask(t, db, 501, 1, "ready", "now", "")
	snSession(t, db, 9001, 1, 500, "working")
	snSession(t, db, 9002, 1, 500, "working")
	if _, err := HideSessionTasks(9001, []int64{501}); err != nil {
		t.Fatalf("hide: %v", err)
	}

	mk := func() []SessionStatusRow {
		return []SessionStatusRow{{ID: 500}, {ID: 501}}
	}

	rows := mk()
	if err := AnnotateSessionStatusHidden(rows, 9001); err != nil {
		t.Fatalf("annotate(9001): %v", err)
	}
	if rows[0].Hidden {
		t.Errorf("task 500 must not be hidden for 9001")
	}
	if !rows[1].Hidden || rows[1].HiddenAt == "" {
		t.Errorf("task 501 must be hidden for 9001 with a timestamp, got %+v", rows[1])
	}

	rows = mk()
	if err := AnnotateSessionStatusHidden(rows, 9002); err != nil {
		t.Fatalf("annotate(9002): %v", err)
	}
	if rows[0].Hidden || rows[1].Hidden {
		t.Errorf("session 9002 must see nothing hidden, got %+v", rows)
	}

	rows = mk()
	if err := AnnotateSessionStatusHidden(rows, 0); err != nil {
		t.Fatalf("annotate(0): %v", err)
	}
	if rows[0].Hidden || rows[1].Hidden {
		t.Errorf("viewer 0 must suppress nothing, got %+v", rows)
	}
}

// TestHideSurvivesStatusTransition pins the lifecycle rule: a hide NEVER expires
// on its own. Nothing in the task's status — `unverified` explicitly included —
// clears it; only `session unhide --task` does.
func TestHideSurvivesStatusTransition(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	snTask(t, db, 500, 1, "underway", "now", "")
	snSession(t, db, 9001, 1, 500, "working")
	if _, err := HideSessionTasks(9001, []int64{500}); err != nil {
		t.Fatalf("hide: %v", err)
	}

	for _, status := range []string{"unverified", "confirmed", "revisit"} {
		if _, err := db.Exec(`UPDATE tasks SET status = ? WHERE id = 500`, status); err != nil {
			t.Fatalf("set status %s: %v", status, err)
		}
		hidden, err := SessionHiddenTasks(9001)
		if err != nil {
			t.Fatalf("SessionHiddenTasks after %s: %v", status, err)
		}
		if _, ok := hidden[500]; !ok {
			t.Errorf("status %q cleared the hide; a hide must never expire on its own", status)
		}
	}
}
